// The server's answers «Состояние» is made of, for its tests: a healthy
// server, and the checks the server's own endpoint answers (placeholders only:
// no real address, name or key).
import type { SelfCheckView, ServerCheck } from '../../../api/selfcheck'
import type { StorageView } from '../../../api/storage'
import type { AddonInfo, CoreUpdate, LibraryStatus, NetCheck, TranscodingView } from '../../../api/types'
import type { HealthInput, Load } from '../healthChecks'

export const ready = <T>(value: T): Load<T> => ({ status: 'ready', value })

export const addon = (over: Partial<AddonInfo> = {}): AddonInfo => ({
  id: 'one',
  name: 'Расширение',
  description: '',
  version: '0.1.0',
  source: 'github.com/a/x',
  enabled: true,
  status: 'running',
  update: null,
  updateState: '',
  pinned: false,
  pinFailed: false,
  ...over,
})

export const core: CoreUpdate = { version: '0.8.28', commit: '', latest: 'v0.8.28', releaseUrl: '', checked: true, newer: false, docker: true }

export const transcoding: TranscodingView = {
  settings: { mode: 'auto', device: '', tonemap: true, hevc: false },
  defaults: { mode: 'auto', device: '', tonemap: true, hevc: false },
  custom: false,
  devices: ['/dev/dri/renderD128'],
  status: { mode: 'vaapi', name: 'VA-API', requested: 'auto', hevc: false, tonemapGpu: true, tonemapCpu: false, probes: [{ mode: 'vaapi', ok: true }], probedAt: '2026-11-19T10:00:00Z' },
  fingerprint: true,
}

export const network: NetCheck[] = [
  { id: 'github', name: 'GitHub', ok: true, ms: 120 },
  { id: 'musicbrainz', name: 'MusicBrainz', ok: true, ms: 340 },
]

export const library: LibraryStatus = {
  running: false,
  queued: false,
  finishedAt: new Date().toISOString(),
  movies: 12,
  episodes: 340,
  albums: 0,
  tracks: 0,
  books: 0,
  audiobooks: 0,
  comics: 0,
  files: 0,
  skipped: 0,
  removed: 0,
}

const dir = (path: string, status: 'ok' | 'readonly' | 'missing' = 'ok', items = 3) => ({ path, status, items })

export const storage: StorageView = {
  root: dir('/media'),
  folders: [{ ...dir('movies'), kind: 'movies', abs: '/media/movies', default: 'movies', primary: true, adult: false, unlisted: false, indexed: 3 }],
  transcode: dir('/transcode'),
  entries: [],
  custom: false,
  restarts: true,
  canCreate: true,
  server: { uid: 1000, gid: 1000 },
  offline: false,
  places: ['/media'],
}

/** The folders the server may not write to, and its fix. */
export const readonlyStorage: StorageView = {
  ...storage,
  folders: [{ ...storage.folders[0]!, ...dir('movies', 'readonly'), abs: '/media/movies', why: 'denied' }],
  fix: { paths: ['/media/movies'], docker: true, uid: 1000, gid: 1000, owner: '1001:1001', chown: 'sudo chown -R 1000:1000 /srv/media/movies' },
}

/** What the server's own endpoint answers on a healthy install (mail and TV not used). */
export const healthyChecks: ServerCheck[] = [
  { id: 'connection', state: 'ok', code: 'home' },
  { id: 'https', state: 'ok', code: 'plainHome' },
  { id: 'address', state: 'ok', code: 'set', params: { host: 'media.example.org' } },
  { id: 'data', state: 'ok', code: 'ok', params: { path: '/config', free: 38 * 2 ** 30 } },
  { id: 'space', state: 'ok', code: 'ok', params: { free: 410 * 2 ** 30, percent: 40 } },
  { id: 'ffmpeg', state: 'ok', code: 'ok', params: { version: '7.1' } },
  { id: 'tmdb', state: 'ok', code: 'set' },
  { id: 'clock', state: 'ok', code: 'ok', params: { zone: 'Europe/Berlin' } },
  { id: 'mail', state: 'off', code: 'unset' },
]

export const serverView = (checks: ServerCheck[] = healthyChecks): Load<SelfCheckView> => ready({ checkedAt: '2026-11-25T14:02:00Z', checks })

/** A healthy server: no addons, every other part answered fine. */
export const healthy = (over: Partial<HealthInput> = {}): HealthInput => ({
  server: serverView(),
  core: ready(core),
  addons: null,
  helpers: null,
  hw: ready(transcoding),
  storage: ready(storage),
  net: ready(network),
  library: ready(library),
  webFailed: new Map(),
  zone: 'Europe/Berlin',
  ...over,
})
