// The server's answers behind «Состояние»'s e2e and its screenshots: healthy,
// or with something wrong in every group, with the longest strings the page
// can meet. Placeholders only (no real address, name or key).
import type { Page } from '@playwright/test'

export const SIZES = [
  { name: '2560x1440', viewport: { width: 2560, height: 1440 }, phone: false },
  { name: '1280x610', viewport: { width: 1280, height: 610 }, phone: false },
  { name: '390x844', viewport: { width: 390, height: 844 }, phone: true },
]
export const LANGS = [
  { id: 'ru', locale: 'ru-RU', title: 'Состояние', broken: 'Соединение', sheetFix: 'Как починить', copy: 'Скопировать' },
  { id: 'en', locale: 'en-US', title: 'Health', broken: 'Connection', sheetFix: 'How to Fix', copy: 'Copy' },
] as const

const GB = 2 ** 30

const OK = [
  { id: 'connection', state: 'ok', code: 'home' },
  { id: 'https', state: 'ok', code: 'plainHome' },
  { id: 'address', state: 'ok', code: 'set', params: { host: 'media.example.org' } },
  { id: 'data', state: 'ok', code: 'ok', params: { path: '/config', free: 38 * GB } },
  { id: 'space', state: 'ok', code: 'ok', params: { free: 1400 * GB, percent: 61 } },
  { id: 'ffmpeg', state: 'ok', code: 'ok', params: { version: '7.1' } },
  { id: 'tmdb', state: 'ok', code: 'accepted', at: '2026-11-25T14:00:00Z' },
  { id: 'clock', state: 'ok', code: 'ok', params: { zone: 'Europe/Berlin' } },
  { id: 'tv', state: 'ok', code: 'ok', params: { count: 2 } },
  { id: 'mail', state: 'off', code: 'unset' },
]

const BROKEN = [
  { id: 'connection', state: 'warn', code: 'proxy', params: { peer: '172.18.0.1' } },
  { id: 'https', state: 'warn', code: 'lost' },
  { id: 'address', state: 'off', code: 'unset' },
  { id: 'data', state: 'warn', code: 'low', params: { path: '/config', free: 1.4 * GB } },
  { id: 'space', state: 'warn', code: 'low', params: { free: 410 * GB, percent: 88 } },
  { id: 'ffmpeg', state: 'warn', code: 'old', params: { version: '6.0' } },
  { id: 'tmdb', state: 'bad', code: 'rejected', at: '2026-11-25T14:00:00Z' },
  { id: 'clock', state: 'warn', code: 'utc', params: { browser: '+1:00' } },
  { id: 'tv', state: 'warn', code: 'failing', params: { name: 'Провайдер с очень длинным названием плейлиста', error: 'HTTP 403', count: 1 } },
  { id: 'mail', state: 'bad', code: 'auth', params: { host: 'smtp.example.com' } },
]

const addon = (over: object) => ({
  description: '',
  source: 'github.com/a/x',
  enabled: true,
  status: 'running',
  update: null,
  updateState: '',
  pinned: false,
  pinFailed: false,
  version: '0.1.13',
  ...over,
})

const dir = (p: string, status = 'ok') => ({ path: p, status, items: 3 })
const folder = (status: string) => ({ ...dir('movies', status), kind: 'movies', abs: '/media/movies', default: 'movies', primary: true, adult: false, unlisted: false, indexed: 3, why: status === 'ok' ? undefined : 'denied' })
const storage = (status: string) => ({
  root: dir('/media'),
  folders: [folder(status)],
  transcode: dir('/transcode'),
  entries: [],
  custom: false,
  restarts: true,
  canCreate: true,
  server: { uid: 1000, gid: 1000 },
  offline: false,
  places: ['/media'],
  fix: status === 'ok' ? undefined : { paths: ['/media/movies'], docker: true, uid: 1000, gid: 1000, owner: '1001:1001', chown: 'sudo chown -R 1000:1000 /srv/media/movies' },
})

const gpu = (broken: boolean) => ({
  settings: { mode: 'auto', device: '', tonemap: true, hevc: false },
  defaults: { mode: 'auto', device: '', tonemap: true, hevc: false },
  custom: false,
  devices: ['/dev/dri/renderD128'],
  status: broken
    ? { mode: 'none', name: 'software', requested: 'auto', hevc: false, tonemapGpu: false, tonemapCpu: true, probes: [{ mode: 'vaapi', ok: false }], problem: { kind: 'permission', mode: 'vaapi', device: '/dev/dri/renderD128', gid: 105 }, probedAt: '2026-11-19T10:00:00Z' }
    : { mode: 'vaapi', name: 'VA-API', requested: 'auto', hevc: false, tonemapGpu: true, tonemapCpu: false, probes: [{ mode: 'vaapi', ok: true }], probedAt: '2026-11-19T10:00:00Z' },
  fingerprint: true,
})

/** The server as it answers: healthy, or with something wrong in every group. */
function world(broken: boolean): Record<string, unknown> {
  return {
    '/api/admin/selfcheck': { checkedAt: '2026-11-25T14:02:00Z', checks: broken ? BROKEN : OK },
    '/api/updates': { version: '0.8.28', commit: '', latest: broken ? 'v0.8.29' : 'v0.8.28', releaseUrl: '', checked: true, newer: broken, docker: true },
    '/api/addons': broken
      ? [
          addon({ id: 'a', name: 'Расширение с очень длинным названием, которое не помещается в строку', status: 'crashed' }),
          addon({ id: 'b', name: 'Второе', update: '0.2.0', version: '0.1.0' }),
          addon({ id: 'c', name: 'Третье', status: 'incompatible', minCore: '0.8.30' }),
        ]
      : [addon({ id: 'a', name: 'Первое' }), addon({ id: 'b', name: 'Второе', version: '0.4.2' })],
    '/api/addons/helpers': broken
      ? [{ addon: 'a', name: 'helper-service', title: 'Служба с длинным названием для проверки', address: 'http://helper-service:8191', ok: false, problem: 'refused' }]
      : [{ addon: 'a', name: 'helper-service', title: 'Решатель', address: 'http://helper-service:8191', ok: true }],
    '/api/settings/transcoding': gpu(broken),
    '/api/settings/storage': storage(broken ? 'readonly' : 'ok'),
    '/api/admin/health/network': [
      { id: 'github', name: 'GitHub', ok: true, ms: 120 },
      { id: 'musicbrainz', name: 'MusicBrainz', ok: !broken, ms: 340, error: broken ? 'context deadline exceeded' : undefined },
    ],
    '/api/settings/external': { services: [], chosen: true, notice: false },
    '/api/library/status': { running: false, queued: false, finishedAt: new Date().toISOString(), movies: 12, episodes: 340, albums: 0, tracks: 0, books: 0, audiobooks: 0, comics: 0, files: 0, skipped: 0, removed: 0 },
  }
}

export async function mock(page: Page, broken: boolean) {
  const answers = world(broken)
  await page.route(
    (u) => u.pathname in answers,
    (route) => route.fulfill({ json: answers[new URL(route.request().url()).pathname] }),
  )
}

/** Every row's name and value apart, inside the row; no row clipped; the page not wider than the window. */
export async function layoutProblems(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out: string[] = []
    const rect = (e: Element) => e.getBoundingClientRect()
    const rows = document.querySelectorAll('section ul > li')
    for (const li of rows) {
      const name = li.querySelector('[class*="label"]')
      const value = li.querySelector('[class*="value"]')
      const where = (li.textContent ?? '').slice(0, 40)
      if (!name) continue
      const l = rect(li)
      const n = rect(name)
      if (n.left < l.left - 1 || n.right > l.right + 1) out.push(`name leaves its row: ${where}`)
      if (name.scrollWidth > name.clientWidth + 1) out.push(`name is cut: ${where}`)
      if (value) {
        const v = rect(value)
        const sideBySide = n.top < v.bottom - 1 && v.top < n.bottom - 1
        if (sideBySide && n.right > v.left + 1 && v.width > 0) out.push(`name and value overlap: ${where}`)
        if (v.right > l.right + 1 || v.left < l.left - 1) out.push(`value leaves its row: ${where}`)
      }
    }
    if (document.documentElement.scrollWidth > window.innerWidth) out.push(`the page scrolls sideways (${document.documentElement.scrollWidth} > ${window.innerWidth})`)
    return out
  })
}
