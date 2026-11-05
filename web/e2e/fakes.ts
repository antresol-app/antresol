// Fake outside world for the metadata and import tests: a TMDB API that
// knows one movie and one aired series, an OPDS catalog with one book,
// MusicBrainz (+ Cover Art Archive) that knows three albums, Open Library
// (+ covers) that knows three books. A build's own e2e adds routes
// (startFakes(routes), vault.ts fakesExtension).
import { readFileSync } from 'node:fs'
import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http'
import type { AddressInfo } from 'node:net'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Page } from '@playwright/test'

const IMAGES = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures', 'images')

export const FAKE_MOVIE = {
  id: 4242,
  title: 'Тестовый фильм',
  original: 'Test Movie',
  year: 2024,
}
/** A TMDB series whose first season has aired (follow.spec: «Следить»). */
export const FAKE_SERIES = {
  id: 4343,
  title: 'Тестовый сериал',
  original: 'Test Show',
  year: 2023,
  episodes: 3,
}
/** A second aired series (migrate.spec: two series taken over from Sonarr). */
export const FAKE_SERIES_2 = {
  id: 4344,
  title: 'Второй тестовый сериал',
  original: 'Second Test Show',
  year: 2022,
  episodes: 2,
}
const FAKE_TV = [FAKE_SERIES, FAKE_SERIES_2]
/** An OPDS catalog at /opds (sources.spec: «OPDS-каталог»). */
export const FAKE_OPDS = { path: '/opds', title: 'Тестовая библиотека' }

/** MusicBrainz release groups (found by «кино»); the first one has a release on the fake site. */
export const FAKE_ALBUMS = [
  {
    id: '0b2a4c1e-1111-4000-8000-000000000001',
    title: 'Группа крови',
    artist: 'Кино',
    year: 1988,
    image: 'elections-poster.jpg',
  },
  {
    id: '0b2a4c1e-1111-4000-8000-000000000002',
    title: 'Звезда по имени Солнце',
    artist: 'Кино',
    year: 1989,
    image: 'garlic-poster.jpg',
  },
  {
    id: '0b2a4c1e-1111-4000-8000-000000000003',
    title: 'Кино (Чёрный альбом)',
    artist: 'Кино',
    year: 1990,
    image: 'wolf-poster.jpg',
  },
]
export const FAKE_ALBUM = FAKE_ALBUMS[0]!

/** Open Library works (found by «мастер» or «булгаков»). */
export const FAKE_BOOKS = [
  {
    key: '/works/OL1000001W',
    title: 'Мастер и Маргарита',
    author: 'Михаил Булгаков',
    year: 1967,
    cover: 101,
    image: 'catchme-poster.jpg',
  },
  {
    key: '/works/OL1000002W',
    title: 'Собачье сердце',
    author: 'Михаил Булгаков',
    year: 1987,
    cover: 102,
    image: 'drain-poster.jpg',
  },
  {
    key: '/works/OL1000003W',
    title: 'Белая гвардия',
    author: 'Михаил Булгаков',
    year: 1925,
    cover: 0,
    image: '',
  },
]

export interface Fakes {
  url: string
  close: () => Promise<void>
}

/**
 * More routes of the fake outside world (a build's own e2e adds them):
 * true when the route answered the request.
 */
export type FakeRoutes = (req: IncomingMessage, res: ServerResponse, u: URL) => boolean

export async function startFakes(routes?: FakeRoutes): Promise<Fakes> {
  const image = (res: ServerResponse, name: string) => {
    res.writeHead(200, { 'Content-Type': 'image/jpeg' })
    res.end(readFileSync(path.join(IMAGES, name)))
  }
  const server: Server = createServer((req, res) => {
    const u = new URL(req.url ?? '/', 'http://x')
    const json = (v: unknown) => {
      res.writeHead(200, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify(v))
    }
    const p = u.pathname
    if (p === '/3/search/movie') {
      const q = (u.searchParams.get('query') ?? '').toLowerCase()
      return json({
        results:
          q.includes('тест') || q.includes('test')
            ? [
                {
                  id: FAKE_MOVIE.id,
                  title: FAKE_MOVIE.title,
                  original_title: FAKE_MOVIE.original,
                  release_date: `${FAKE_MOVIE.year}-03-01`,
                  overview: 'Фильм для проверки поиска.',
                  poster_path: '',
                },
              ]
            : [],
      })
    }
    if (p === '/3/search/tv') return json({ results: [] })
    const tv = /^\/3\/tv\/(\d+)(\/season\/1)?$/.exec(p)
    const show = tv ? FAKE_TV.find((x) => x.id === Number(tv[1])) : undefined
    if (show && !tv![2]) {
      return json({
        id: show.id,
        name: show.title,
        original_name: show.original,
        first_air_date: `${show.year}-02-01`,
        overview: 'Сериал для проверки слежения.',
        genres: [{ name: 'Драма' }],
        episode_run_time: [45],
        seasons: [{ season_number: 1, episode_count: show.episodes }],
      })
    }
    if (show) {
      // aired long ago: every episode is due
      return json({
        season_number: 1,
        air_date: `${show.year}-02-01`,
        episodes: Array.from({ length: show.episodes }, (_, i) => ({
          episode_number: i + 1,
          name: `Серия ${i + 1}`,
          overview: '',
          still_path: null,
          air_date: `${show.year}-02-${String(1 + i * 7).padStart(2, '0')}`,
        })),
      })
    }
    if (p === `/3/movie/${FAKE_MOVIE.id}`) {
      return json({
        id: FAKE_MOVIE.id,
        title: FAKE_MOVIE.title,
        original_title: FAKE_MOVIE.original,
        release_date: `${FAKE_MOVIE.year}-03-01`,
        overview: 'Фильм для проверки поиска.',
        genres: [{ name: 'Драма' }],
        runtime: 101,
      })
    }
    if (p.startsWith('/3/')) {
      res.writeHead(404, { 'Content-Type': 'application/json' })
      return res.end('{"status_code":34}')
    }
    // MusicBrainz: a free-text release group search (the library's own lookups find nothing)
    if (p === '/ws/2/release-group') {
      const q = (u.searchParams.get('query') ?? '').toLowerCase()
      const hit = !q.includes('releasegroup:') && (q.includes('кино') || q.includes('крови'))
      return json({
        'release-groups': hit
          ? FAKE_ALBUMS.map((a) => ({
              id: a.id,
              title: a.title,
              score: 100,
              'first-release-date': `${a.year}-01-05`,
              'artist-credit': [
                {
                  name: a.artist,
                  joinphrase: '',
                  artist: { id: 'artist-kino', name: a.artist },
                },
              ],
            }))
          : [],
      })
    }
    const caa = /^\/caa\/release-group\/([^/]+)\/front-500$/.exec(p)
    if (caa) {
      const a = FAKE_ALBUMS.find((x) => x.id === caa[1])
      if (a) return image(res, a.image)
    }
    // Open Library: a free-text search (q); title/author lookups of the library find nothing
    if (p === '/ol/search.json') {
      const q = (u.searchParams.get('q') ?? '').toLowerCase()
      const hit = q.includes('мастер') || q.includes('булгаков')
      return json({
        docs: hit
          ? FAKE_BOOKS.map((b) => ({
              key: b.key,
              title: b.title,
              author_name: [b.author],
              first_publish_year: b.year,
              cover_i: b.cover || undefined,
            }))
          : [],
      })
    }
    const olc = /^\/olc\/b\/id\/(\d+)-L\.jpg$/.exec(p)
    if (olc) {
      const b = FAKE_BOOKS.find((x) => x.cover === Number(olc[1]))
      if (b?.image) return image(res, b.image)
    }
    // OPDS 1.2: a root feed with an OpenSearch link, one book in search results
    const base = `http://${req.headers.host}`
    if (p === FAKE_OPDS.path) {
      res.writeHead(200, {
        'Content-Type': 'application/atom+xml;profile=opds-catalog;kind=navigation',
      })
      return res.end(`<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>urn:fake:opds</id>
  <title>${FAKE_OPDS.title}</title>
  <updated>2026-01-01T00:00:00Z</updated>
  <link rel="self" href="${base}/opds" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>
  <link rel="start" href="${base}/opds" type="application/atom+xml;profile=opds-catalog;kind=navigation"/>
  <link rel="search" href="${base}/opds/opensearch.xml" type="application/opensearchdescription+xml"/>
  <entry>
    <id>urn:fake:new</id>
    <title>Новинки</title>
    <updated>2026-01-01T00:00:00Z</updated>
    <link rel="subsection" href="${base}/opds/search?q=" type="application/atom+xml;profile=opds-catalog;kind=acquisition"/>
  </entry>
</feed>`)
    }
    if (p === '/opds/opensearch.xml') {
      res.writeHead(200, {
        'Content-Type': 'application/opensearchdescription+xml',
      })
      return res.end(`<?xml version="1.0" encoding="utf-8"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>${FAKE_OPDS.title}</ShortName>
  <Url type="application/atom+xml;profile=opds-catalog;kind=acquisition" template="${base}/opds/search?q={searchTerms}"/>
</OpenSearchDescription>`)
    }
    if (p === '/opds/search') {
      res.writeHead(200, {
        'Content-Type': 'application/atom+xml;profile=opds-catalog;kind=acquisition',
      })
      const b = FAKE_BOOKS[0]!
      return res.end(`<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:dc="http://purl.org/dc/terms/">
  <id>urn:fake:search</id>
  <title>Поиск</title>
  <updated>2026-01-01T00:00:00Z</updated>
  <entry>
    <id>urn:fake:book:1</id>
    <title>${b.title}</title>
    <author><name>${b.author}</name></author>
    <dc:language>ru</dc:language>
    <updated>2026-01-01T00:00:00Z</updated>
    <link rel="http://opds-spec.org/acquisition" href="${base}/opds/book/1.epub" type="application/epub+zip"/>
  </entry>
</feed>`)
    }
    if (routes?.(req, res, u)) return
    res.writeHead(404)
    res.end()
  })
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r))
  const { port } = server.address() as AddressInfo
  return {
    url: `http://127.0.0.1:${port}`,
    close: () => new Promise((r) => server.close(() => r())),
  }
}

/**
 * «Импорт» files for import.spec and the screenshots: a CSV in the universal
 * format — 12 songs of 4 albums, 2 playlists, the rest liked.
 */
export const IMPORT_CSV = [
  'artist,title,album,isrc,playlist,position,dateAdded',
  'Кино,Группа крови,Группа крови,,Дорога,1,',
  'Кино,Закрой за мной дверь,Группа крови,,Дорога,2,',
  'Кино,Война,Группа крови,,Дорога,3,',
  'Кино,Звезда по имени Солнце,Звезда по имени Солнце,,Дорога,4,',
  'Кино,Пачка сигарет,Звезда по имени Солнце,,Вечер,1,',
  'Кино,Апрель,Звезда по имени Солнце,,Вечер,2,',
  'Аквариум,Радио Африка,Радио Африка,,Вечер,3,',
  'Аквариум,Капитан Африка,Радио Африка,,,,2024-03-01T10:00:00Z',
  'Аквариум,Рок-н-ролл мёртв,Радио Африка,,,,2024-03-02T10:00:00Z',
  'ДДТ,Актриса Весна,Актриса Весна,,,,2024-03-03T10:00:00Z',
  'ДДТ,Родина,Актриса Весна,,,,2024-03-04T10:00:00Z',
  'Кино,Кукушка,,,,,2024-03-05T10:00:00Z',
].join('\n')

type Json = Record<string, unknown>

/**
 * After «Начать импорт», the job's run on the server is played by page
 * routes (no MusicBrainz or download client in e2e): every load of the job moves it
 * on — matching, then downloading with one song waiting for a decision —
 * and once that song is resolved the job is done. `base` is the real job of
 * the preview (POST /api/import/files).
 */
export async function fakeImportRun(page: Page, base: Json, opts: { poster?: string } = {}) {
  const id = base.id as number
  const total = (base.progress as { total: number }).total
  let started = false
  let stage = 0 // 0 matching, 1 downloading (one in review), 2 done
  let resolved = false
  let retried = false
  const progress = (o: Json) => ({
    total,
    matched: 0,
    review: 0,
    notFound: 0,
    have: 0,
    queued: 0,
    downloading: 0,
    done: 0,
    failed: 0,
    skipped: 0,
    ...o,
  })
  const job = () => {
    if (stage === 0)
      return {
        ...base,
        status: 'matching',
        progress: progress({ matched: 5, have: 2 }),
      }
    if (stage === 1)
      return {
        ...base,
        status: 'downloading',
        progress: progress({
          have: 3,
          done: 3,
          downloading: 2,
          queued: 2,
          notFound: 1,
          review: 1,
        }),
      }
    return {
      ...base,
      status: 'done',
      progress: progress({
        have: 3,
        done: resolved ? 8 : 7,
        notFound: 1,
        skipped: resolved ? 0 : 1,
      }),
    }
  }
  const group = (o: Json) => ({
    year: 0,
    songs: 1,
    bytes: 0,
    done: 0,
    error: '',
    infoHash: '',
    ...o,
  })
  const groups = () =>
    stage === 0
      ? []
      : [
          group({
            id: 1,
            artist: 'Кино',
            album: 'Группа крови',
            year: 1988,
            songs: 3,
            state: stage === 1 ? 'downloading' : 'done',
            bytes: 312 << 20,
            done: stage === 1 ? 131 << 20 : 312 << 20,
          }),
          group({
            id: 2,
            artist: 'Кино',
            album: 'Звезда по имени Солнце',
            year: 1989,
            songs: 3,
            state: 'have',
          }),
          group({
            id: 3,
            artist: 'Аквариум',
            album: 'Радио Африка',
            year: 1983,
            songs: 3,
            state: stage === 1 ? 'searching' : 'done',
            bytes: stage === 1 ? 0 : 240 << 20,
            done: stage === 1 ? 0 : 240 << 20,
          }),
          group({
            id: 4,
            artist: 'ДДТ',
            album: 'Актриса Весна',
            year: 1992,
            songs: 2,
            state: retried && stage === 1 ? 'waiting' : 'not_found',
          }),
        ]
  const candidate = {
    tmdbId: 0,
    extId: '0b2a4c1e-1111-4000-8000-000000000003',
    kind: 'music',
    title: 'Кино (Чёрный альбом)',
    subtitle: 'Кино',
    year: 1990,
    poster: opts.poster ?? '',
    overview: '',
    inLibrary: false,
    libraryItemId: null,
    matchedTrack: 'Кукушка',
  }
  const review = () =>
    stage >= 1 && !resolved
      ? [
          {
            id: 901,
            artist: 'Кино',
            title: 'Кукушка',
            album: '',
            reason: 'Несколько исполнителей с таким именем',
            candidates: [],
          },
        ]
      : []
  const json = (body: unknown, status = 200) => ({
    status,
    contentType: 'application/json',
    body: JSON.stringify(body),
  })

  await page.route(new RegExp(`/api/import/jobs/${id}/start$`), (r) => {
    started = true
    return r.fulfill({ status: 204, body: '' })
  })
  await page.route(new RegExp(`/api/import/jobs/${id}$`), (r) => {
    if (!started || r.request().method() !== 'GET') return r.continue()
    if (stage === 1 && resolved) stage = 2
    const out = job()
    if (stage === 0) stage = 1
    return r.fulfill(json(out))
  })
  await page.route(new RegExp(`/api/import/jobs/${id}/groups$`), (r) => r.fulfill(json({ groups: groups() })))
  await page.route(new RegExp(`/api/import/jobs/${id}/review$`), (r) => r.fulfill(json({ tracks: review() })))
  await page.route(/\/api\/import\/tracks\/901\/candidates$/, (r) => r.fulfill(json({ candidates: [candidate] })))
  await page.route(/\/api\/import\/search\?/, (r) => r.fulfill(json({ candidates: [candidate] })))
  await page.route(/\/api\/import\/tracks\/901\/resolve$/, (r) => {
    resolved = true
    return r.fulfill({ status: 204, body: '' })
  })
  await page.route(/\/api\/import\/groups\/\d+\/retry$/, (r) => {
    retried = true
    return r.fulfill({ status: 204, body: '' })
  })
  // the job list (a reload) shows the played run too
  await page.route(/\/api\/import\/jobs$/, async (r) => {
    if (!started || r.request().method() !== 'GET') return r.continue()
    return r.fulfill(json({ jobs: [job()] }))
  })
}
