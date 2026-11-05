// Per-test bin/vault: every test gets its own server process, data dir and
// media root on a free port, so tests never depend on each other's state.
import { spawn, type ChildProcess } from 'node:child_process'
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { seedBooks } from './books-media'
import { seedComicsAudio } from './comics-audio-media'
import { startFakes, type FakeRoutes, type Fakes } from './fakes'
import { seedFiles } from './files-media'
import { seedPlayback } from './media'
import { seedMigrate } from './migrate-media'
import { seedMusic } from './music-media'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { test as base, expect, type APIRequestContext } from '@playwright/test'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const BIN = path.join(HERE, '..', '..', 'bin', 'vault')
const IMAGES = path.join(HERE, 'fixtures', 'images')

/**
 * More variables for the test server when a build has its own: a JSON object
 * in vault.env.full.json beside this file, where «{importDir}» in a value is
 * the test's import folder.
 */
function buildEnv(importDir: string): Record<string, string> {
  const file = path.join(HERE, 'vault.env.full.json')
  if (!existsSync(file)) return {}
  const env = JSON.parse(readFileSync(file, 'utf8')) as Record<string, string>
  return Object.fromEntries(Object.entries(env).map(([k, v]) => [k, v.replaceAll('{importDir}', importDir)]))
}

/** The test movie that the sample library contains. */
export const LIBRARY_MOVIE = { title: 'Интерстеллар', year: 2014 }
export const ADMIN = { name: 'Александр', password: 'семья-2026' }

/**
 * empty, sample (fake file, artwork only), playback (real generated videos, see media.ts),
 * files (files-media.ts), music (music-media.ts) or books (books-media.ts and
 * comics-audio-media.ts),
 * the last two plus the sample movie; all = music + books + the sample movie.
 */
export type Library = 'empty' | 'sample' | 'playback' | 'files' | 'music' | 'books' | 'all'

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const s = createServer()
    s.once('error', reject)
    s.listen(0, '127.0.0.1', () => {
      const { port } = s.address() as { port: number }
      s.close(() => resolve(port))
    })
  })
}

/** Lays out a media root the scanner understands, with real local artwork. */
function seedMedia(root: string, library: Library) {
  const movies = path.join(root, 'media', 'movies')
  mkdirSync(movies, { recursive: true })
  mkdirSync(path.join(root, 'media', 'series'), { recursive: true })
  if (library === 'empty') return
  if (library === 'playback') return seedPlayback(root, IMAGES)
  if (library === 'files') return seedFiles(root, IMAGES)
  if (library === 'music' || library === 'all') seedMusic(root, IMAGES)
  if (library === 'books' || library === 'all') {
    seedBooks(root, IMAGES)
    seedComicsAudio(root, IMAGES)
  }
  const dir = path.join(movies, `${LIBRARY_MOVIE.title} (${LIBRARY_MOVIE.year})`)
  mkdirSync(dir, { recursive: true })
  // The scanner indexes by name and size; no decoding happens during a scan.
  writeFileSync(path.join(dir, `Interstellar.${LIBRARY_MOVIE.year}.1080p.BluRay.mkv`), Buffer.alloc(4096))
  copyFileSync(path.join(IMAGES, 'interstellar-poster.jpg'), path.join(dir, 'poster.jpg'))
  copyFileSync(path.join(IMAGES, 'interstellar-backdrop.jpg'), path.join(dir, 'fanart.jpg'))
}

async function waitHealthy(url: string, proc: ChildProcess, log: () => string) {
  const until = Date.now() + 15_000
  while (Date.now() < until) {
    if (proc.exitCode !== null) throw new Error(`bin/vault exited (${proc.exitCode}):\n${log()}`)
    try {
      if ((await fetch(`${url}/api/health`)).ok) return
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 100))
  }
  throw new Error(`bin/vault did not start:\n${log()}`)
}

export interface ScanCounts {
  movies: number
  episodes: number
  albums: number
  tracks: number
  books: number
  audiobooks: number
  comics: number
}

/** Runs a library scan through the API (admin) and waits until it finishes with `until` true. */
export async function scanLibrary(request: APIRequestContext, until: (s: ScanCounts) => boolean = (s) => s.movies > 0) {
  expect((await request.post('/api/library/scan')).ok()).toBeTruthy()
  await expect
    .poll(
      async () => {
        const s = (await (await request.get('/api/library/status')).json()) as ScanCounts & { running: boolean; queued: boolean }
        return !s.running && !s.queued && until(s)
      },
      { timeout: 15_000 },
    )
    .toBe(true)
}

/** Creates the administrator through the first-run API (signs the context in). */
export async function setupAdmin(request: APIRequestContext) {
  const r = await request.post('/api/setup', { data: ADMIN })
  expect(r.ok(), await r.text()).toBeTruthy()
}

/** The fake outside world (fakes.ts) of a running Vault, by its URL (fakes: true only). */
const fakeURLs = new Map<string, string>()
export const fakeURLOf = (vaultURL: string) => fakeURLs.get(vaultURL) ?? ''

/**
 * What a build's own e2e adds to the fake outside world (fakes: true): more
 * routes, and files written into the data folder before the server starts.
 */
export interface FakesExtension {
  routes?: FakeRoutes
  prepare?: (dataDir: string, fakeURL: string) => void
}

export const test = base.extend<{
  library: Library
  fakes: boolean
  migrate: boolean
  fakesExtension: FakesExtension | null
  vaultURL: string
}>({
  library: ['sample', { option: true }],
  /** Old Sonarr, Radarr and Jellyfin databases under VAULT_IMPORT_DIR (migrate-media.ts) for «Переезд». */
  migrate: [false, { option: true }],
  /** Fake TMDB, MusicBrainz and Open Library (fakes.ts), plus fakesExtension. */
  fakes: [false, { option: true }],
  fakesExtension: [null, { option: true }],
  vaultURL: async ({ library, fakes, migrate, fakesExtension }, use) => {
    const root = mkdtempSync(path.join(tmpdir(), 'vault-e2e-'))
    const media = path.join(root, 'media-root')
    const data = path.join(root, 'data')
    // always its own (empty unless migrate): the host's /import is never read
    const importDir = path.join(root, 'import')
    mkdirSync(data, { recursive: true })
    mkdirSync(importDir, { recursive: true })
    seedMedia(media, library)
    if (migrate) seedMigrate(importDir, media)
    let fake: Fakes | null = null
    const extra: Record<string, string> = {}
    if (fakes) {
      fake = await startFakes(fakesExtension?.routes)
      fakesExtension?.prepare?.(data, fake.url)
      Object.assign(extra, {
        VAULT_TMDB_KEY: 'test',
        VAULT_TMDB_BASEURL: `${fake.url}/3`,
        VAULT_TMDB_IMAGEBASE: `${fake.url}/images`,
        VAULT_MUSICBRAINZ_BASEURL: `${fake.url}/ws/2`,
        VAULT_COVERART_BASEURL: `${fake.url}/caa`,
        VAULT_OPENLIBRARY_BASEURL: `${fake.url}/ol`,
        VAULT_OPENLIBRARY_COVERS_BASEURL: `${fake.url}/olc`,
      })
    }
    const port = await freePort()
    let out = ''
    const proc = spawn(BIN, [], {
      env: {
        ...process.env,
        VAULT_LISTEN: `127.0.0.1:${port}`,
        VAULT_DATA_DIR: data,
        VAULT_MEDIA_ROOT: media,
        // the fixtures' downloads folder (files-media.ts)
        VAULT_TORRENTS_DIR: 'downloads',
        VAULT_TORRENT_PORT: String(await freePort()),
        // a random port must never be mapped on the real router by UPnP
        VAULT_TORRENT_UPNP: 'off',
        VAULT_TMDB_KEY: '',
        // e2e never reaches the internet: music and book metadata sources off
        // (fakes: the fake MusicBrainz / Open Library below)
        VAULT_MUSICBRAINZ_BASEURL: 'off',
        VAULT_COVERART_BASEURL: 'off',
        VAULT_OPENLIBRARY_BASEURL: 'off',
        VAULT_OPENLIBRARY_COVERS_BASEURL: 'off',
        VAULT_WIKIDATA_BASEURL: 'off',
        VAULT_IMDB_RATINGS_URL: 'off',
        VAULT_SEGMENTS_ONLINE: 'off',
        VAULT_OPENSUBTITLES_BASEURL: 'off',
        VAULT_OPENSUBTITLES_KEY: '',
        VAULT_IMPORT_DIR: importDir,
        ...buildEnv(importDir),
        // the Jellyfin-compatible API is deferred; parallel workers would all
        // take :8096
        VAULT_JELLYFIN_LISTEN: 'off',
        ...extra,
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    proc.stdout?.on('data', (b) => (out += String(b)))
    proc.stderr?.on('data', (b) => (out += String(b)))
    const url = `http://127.0.0.1:${port}`
    try {
      await waitHealthy(url, proc, () => out)
      if (fake) fakeURLs.set(url, fake.url)
      await use(url)
    } finally {
      fakeURLs.delete(url)
      proc.kill('SIGTERM')
      await new Promise((r) => (proc.exitCode !== null ? r(null) : proc.once('exit', r)))
      await fake?.close()
      rmSync(root, { recursive: true, force: true })
    }
  },
  baseURL: async ({ vaultURL }, use) => use(vaultURL),
})

export { expect }
