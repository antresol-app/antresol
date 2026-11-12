// «ТВ» round 5's mocked API (tv-r5.spec.ts and its screenshots): four
// channels (two with an uploaded logo), «Много голосов» with 8 voices and 6
// subtitle languages on air, the editor's options, search and folders.
import { devices, type Browser, type Page } from '@playwright/test'
import { setupAdmin } from './vault'

const iso = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString()
const LOGO = `<svg xmlns="http://www.w3.org/2000/svg" width="240" height="120" viewBox="0 0 240 120"><text x="120" y="78" font-family="Helvetica, Arial" font-weight="700" font-size="56" fill="#fff" text-anchor="middle">КИНО</text></svg>`

const AUDIO = [
  ['Дубляж', 'ru', 'Русский — Дубляж'],
  ['LostFilm', 'ru', 'Русский — LostFilm'],
  ['Кураж-Бамбей', 'ru', 'Русский — Кураж-Бамбей'],
  ['LostFilm (очищено)', 'ru', 'Русский — LostFilm (очищено)'],
  ['Пифагор', 'ru', 'Русский — Пифагор'],
  ['Оригинал (English)', 'en', 'Оригинал (English)'],
  ['Français', 'fr', 'Français'],
  ['Комментарий режиссёра', 'en', 'English — Комментарий режиссёра'],
].map(([name, lang, label]) => ({ name, lang, label }))
const SUBS = ['Русские', 'Русские (полные)', 'English', 'English SDH', 'Français', 'Deutsch'].map((name, i) => ({ name, label: name, lang: ['ru', 'ru', 'en', 'en', 'fr', 'de'][i]! }))

const program = (ch: number, k: number, from: number, len: number) => ({
  id: `${ch}-${k}`, itemId: 100 + k, kind: 'movie', title: k % 2 ? 'Семь дорожек' : 'Много голосов', year: 2015, genres: ['драма'],
  start: iso(from), end: iso(from + len), audio: AUDIO, subtitles: SUBS,
})
export const channels = ['Кино', 'Сериалы', 'Детям', 'Документальное'].map((name, i) => {
  const now = program(i + 1, 0, -20, 110)
  return {
    id: i + 1, number: i + 1, name, category: 'Кино', personal: false, auto: false, enabled: true, canEdit: true, favorite: false,
    logoUrl: i % 2 === 0 ? `/api/channels/${i + 1}/logo.png?v=1` : undefined,
    rules: i === 0 ? { order: 'ordered', picks: [{ kind: 'title', id: 1 }, { kind: 'item', id: 55 }, { kind: 'folder', path: 'series/Друзья/Season 02' }, { kind: 'title', id: 2 }] } : { order: 'shuffle' },
    now, next: program(i + 1, 1, 90, 100),
  }
})

export async function mock(page: Page) {
  const list = { channels, lastChannel: 1, canManage: true, canCreate: true }
  await page.route('**/api/channels?**', (r) => r.fulfill({ json: list }))
  await page.route(/\/api\/channels$/, (r) => r.fulfill({ json: list }))
  await page.route(/\/api\/channels\/\d+\/logo\.png/, (r) => r.fulfill({ contentType: 'image/svg+xml', body: LOGO }))
  await page.route(/\/api\/channels\/\d+\/guide/, (r) => {
    const id = Number(/channels\/(\d+)\/guide/.exec(r.request().url())![1])
    const c = channels.find((x) => x.id === id)!
    return r.fulfill({ json: { channel: c, programs: [c.now, c.next] } })
  })
  await page.route('**/api/channels/epg**', (r) => r.fulfill({ json: { from: iso(-60), to: iso(300), channels: channels.map((c) => ({ ...c, logoUrl: undefined, programs: [c.now, c.next] })) } }))
  await page.route('**/api/channels/live/**', (r) => r.fulfill({ status: 503, json: { error: 'Канал запускается' } }))
  await page.route('**/api/channels/last', (r) => r.fulfill({ status: 204 }))
  await page.route('**/api/channels/options', (r) =>
    r.fulfill({
      json: {
        genres: [], decades: [], profiles: [],
        titles: [
          { id: 1, kind: 'series', title: 'Друзья', year: 1994, episodes: 236 },
          { id: 2, kind: 'movie', title: 'Много голосов', year: 2015 },
        ],
      },
    }),
  )
  await page.route('**/api/channels/options/search**', (r) =>
    r.fulfill({ json: { results: [{ kind: 'movie', id: 3, title: 'Семь дорожек', year: 2018 }, { kind: 'episode', id: 77, title: 'Друзья', subtitle: 'S1 E7 · Отключение', year: 1994 }, { kind: 'series', id: 4, title: 'Друзья навсегда', year: 2001 }] } }),
  )
  await page.route('**/api/channels/options/folders**', (r) => {
    const path = new URL(r.request().url()).searchParams.get('path') ?? ''
    return r.fulfill({ json: path ? { folders: [{ path: `${path}/Season 01`, name: 'Season 01' }], files: [{ id: 90, name: 'Пилот.mkv' }] } : { folders: [{ path: 'series', name: 'series' }, { path: 'movies', name: 'movies' }], files: [] } })
  })
  await page.route('**/api/items/55', (r) => r.fulfill({ json: { id: 55, kind: 'episode', title: 'Друзья', subtitle: 'S2 E1 · Тот, где…', year: 1995 } }))
}

export async function open(browser: Browser, baseURL: string, width: number) {
  const phone = width < 700
  const { defaultBrowserType: _, ...iphone } = devices['iPhone 13']
  const ctx = await browser.newContext(phone ? { ...iphone, viewport: { width, height: 812 }, baseURL, locale: 'ru-RU', colorScheme: 'dark' } : { viewport: { width, height: 900 }, baseURL, locale: 'ru-RU', colorScheme: 'dark' })
  const page = await ctx.newPage()
  await setupAdmin(page.request)
  await mock(page)
  return page
}

