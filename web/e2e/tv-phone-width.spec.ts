// «ТВ» on a phone: the channel list, the guide and the live player keep
// every element inside the screen's width at 375 and 430 pt (an iPhone
// SE/mini and a Pro Max). The player's buttons used to run past the right
// edge at 375; the guide's channel column hyphenated names. Elements inside
// a sideways scroller (the guide grid, the filter row, the rails) are that
// scroller's business; a clip as wide as the screen (the player's root)
// does not excuse a control cut at the edge.
import { devices, type Browser, type Page } from '@playwright/test'
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'empty' })

const iso = (minutes: number) => new Date(Date.now() + minutes * 60_000).toISOString()
const THEMES = ['Кино и сериалы', 'Кино по жанрам', 'Кино по десятилетиям', 'Детям', 'Документальное']
const NAMES = ['Все фильмы', 'Комедии', 'Документальное', 'Мультфильмы', 'Семейное кино', 'Кино 2000-х', 'Две девицы на мели', 'Форс-мажоры']
const TITLES = ['Поймай меня, если сможешь', 'Волк с Уолл-стрит', 'Москва слезам не верит', 'Два брата', 'Планета Земля II']

const program = (ch: number, k: number, from: number, len: number) => ({
  id: `${ch}-${k}`, itemId: 100 + k, kind: 'movie', title: TITLES[(ch + k) % TITLES.length], year: 2001, genres: ['драма'],
  start: iso(from), end: iso(from + len),
  audio: [{ slot: 'ru', name: 'Русский', label: 'Русский — Дубляж', lang: 'ru' }, { slot: 'en', name: 'English', label: 'English', lang: 'en' }],
  subtitles: [{ slot: 'ru', name: 'Русские', label: 'Русские', lang: 'ru' }],
})
const programs = (ch: number) => {
  const out = []
  let t = -180 + ((ch * 17) % 60)
  for (let k = 0; t < 600; k++) {
    const len = 45 + ((ch * 37 + k * 53) % 86)
    out.push(program(ch, k, t, len))
    t += len
  }
  return out
}
const channels = NAMES.map((name, i) => {
  const ps = programs(i + 1)
  const now = ps.findIndex((p) => Date.parse(p.end) > Date.now())
  return { id: i + 1, number: i + 1, name, category: THEMES[i % THEMES.length], personal: false, auto: true, enabled: true, canEdit: false, favorite: i % 3 === 0, now: ps[now], next: ps[now + 1], all: ps }
})

async function mockChannels(page: Page) {
  await page.route('**/api/channels?**', (r) => r.fulfill({ json: { channels: channels.map(({ all: _, ...c }) => c), lastChannel: 2, canManage: true, canCreate: true } }))
  await page.route(/\/api\/channels$/, (r) => r.fulfill({ json: { channels: channels.map(({ all: _, ...c }) => c), lastChannel: 2, canManage: true, canCreate: true } }))
  await page.route('**/api/channels/epg**', (r) =>
    r.fulfill({ json: { from: iso(-60), to: iso(300), channels: channels.map((c) => ({ id: c.id, number: c.number, name: c.name, category: c.category, favorite: c.favorite, programs: c.all })) } }),
  )
  await page.route(/\/api\/channels\/\d+\/guide/, (r) => {
    const id = Number(/channels\/(\d+)\/guide/.exec(r.request().url())![1])
    const c = channels.find((x) => x.id === id)!
    return r.fulfill({ json: { channel: { ...c, all: undefined }, programs: c.all.filter((p) => Date.parse(p.end) > Date.now()) } })
  })
  await page.route('**/api/channels/live/**', (r) => r.fulfill({ status: 503, json: { error: 'Канал запускается' } }))
  await page.route('**/api/channels/last', (r) => r.fulfill({ status: 204 }))
}

async function phonePage(browser: Browser, baseURL: string, width: number) {
  const { defaultBrowserType: _, ...phone } = devices['iPhone 13']
  const ctx = await browser.newContext({ ...phone, viewport: { width, height: width === 375 ? 812 : 932 }, baseURL, locale: 'ru-RU', colorScheme: 'dark' })
  const page = await ctx.newPage()
  await setupAdmin(page.request)
  await mockChannels(page)
  return page
}

/** Elements outside the screen's width, not inside a sideways scroller or a narrower clip of their own. */
const outside = () => {
  const W = window.innerWidth
  const out: string[] = []
  if (document.documentElement.scrollWidth > W + 1) out.push(`page ${document.documentElement.scrollWidth}`)
  for (const el of document.querySelectorAll('body *')) {
    const r = el.getBoundingClientRect()
    if (!r.width || !r.height || (r.left >= -1 && r.right <= W + 1)) continue
    const cs = getComputedStyle(el)
    if (cs.visibility === 'hidden' || cs.display === 'none') continue
    let a = el.parentElement
    let clipped = false
    while (a && a !== document.body) {
      const s = getComputedStyle(a)
      const ar = a.getBoundingClientRect()
      const inside = ar.left >= -1 && ar.right <= W + 1
      if (inside && (/(auto|scroll)/.test(s.overflowX) || (/(hidden|clip)/.test(s.overflowX) && (ar.left > 1 || ar.right < W - 1)))) {
        clipped = true
        break
      }
      a = a.parentElement
    }
    if (!clipped) out.push(`${el.tagName.toLowerCase()}.${String(el.className).slice(0, 40)} [${Math.round(r.left)}, ${Math.round(r.right)}]`)
  }
  return out
}

for (const width of [375, 430]) {
  test(`«ТВ» at ${width} pt: the list, the guide and the player stay inside the screen`, async ({ browser, browserName, vaultURL }) => {
    test.skip(browserName !== 'chromium', 'mobile Chromium: the phone layout and its layout viewport')
    const page = await phonePage(browser, vaultURL, width)

    await page.goto('/tv')
    await expect(page.getByRole('list', { name: 'Каналы' }).getByRole('link').first()).toBeVisible()
    expect(await page.evaluate(outside)).toEqual([])

    await page.goto('/tv?view=guide')
    await expect(page.locator('[data-cell]').first()).toBeVisible()
    await page.getByTestId('guide').evaluate((el) => el.scrollBy(200, 0))
    await page.waitForTimeout(300)
    expect(await page.evaluate(outside)).toEqual([])
    // the channel column: number and name, no hyphens
    const hyphens = await page.locator('[role=rowheader]').evaluateAll((els) => els.map((e) => getComputedStyle(e.querySelector('span:nth-child(2)')!).hyphens))
    expect(hyphens.every((h) => h !== 'auto')).toBe(true)

    await page.goto('/tv/2')
    await expect(page.getByTestId('live-player')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Звук и субтитры' })).toBeVisible()
    await page.waitForTimeout(500)
    expect(await page.evaluate(outside)).toEqual([])
    // upright: the picture right under the header, no empty band above it
    const pic = await page.locator('video').boundingBox()
    expect(pic!.y).toBeLessThan(140)
    expect(pic!.height).toBeLessThan(width * 0.6)
    await page.context().close()
  })
}
