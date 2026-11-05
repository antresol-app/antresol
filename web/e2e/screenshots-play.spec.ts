// Reference screenshots of Фильмы, the item page and the player, against
// bin/vault with the generated playback library. The item JSON is enriched
// with the metadata TMDB would add (overview, genres, cast, logo) so the
// pictures show a complete page. Opt-in:
//   SCREENSHOTS=1 npx playwright test e2e/screenshots-play.spec.ts --project chromium
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Browser, Page } from '@playwright/test'
import { PLAYBACK } from './media'
import { expect, setupAdmin, test } from './vault'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const OUT = path.join(HERE, '..', '..', 'docs', 'screenshots')
const IMAGES = path.join(HERE, 'fixtures', 'images')
const shot = (page: Page, name: string) => page.screenshot({ path: path.join(OUT, `${name}.jpg`), type: 'jpeg', quality: 85 })

test.skip(!process.env.SCREENSHOTS, 'set SCREENSHOTS=1 to refresh docs/screenshots')
test.use({ library: 'playback' })
test.describe.configure({ timeout: 180_000 })

const VIEWPORTS = [
  { tag: 'desktop', width: 1440, height: 900, deviceScaleFactor: 2, isMobile: false },
  { tag: 'phone', width: 390, height: 844, deviceScaleFactor: 3, isMobile: true },
] as const

const ENRICH: Record<string, Record<string, unknown>> = {
  [PLAYBACK.direct.title]: {
    title: 'Интерстеллар',
    originalTitle: 'Interstellar',
    year: 2014,
    runtime: 169,
    rating: '12+',
    genres: ['Фантастика', 'Драма', 'Приключения'],
    overview:
      'Когда засуха, пыльные бури и вымирание растений приводят человечество к продовольственному кризису, коллектив исследователей и учёных отправляется сквозь червоточину в путешествие, чтобы превзойти прежние ограничения для космических путешествий человека и найти планету с подходящими для человечества условиями.',
    cast: [
      { name: 'Мэттью Макконахи', character: 'Купер' },
      { name: 'Энн Хэтэуэй', character: 'Брэнд' },
      { name: 'Джессика Честейн', character: 'Мёрф' },
      { name: 'Майкл Кейн', character: 'профессор Брэнд' },
      { name: 'Кейси Аффлек', character: 'Том' },
      { name: 'Маккензи Фой', character: 'юная Мёрф' },
      { name: 'Мэтт Дэймон', character: 'доктор Манн' },
    ],
  },
  [PLAYBACK.series.title]: {
    logo: '/api/images/pe2-logo.png',
    rating: '6+',
    genres: ['Документальный', 'Природа'],
    overview:
      'Дэвид Аттенборо показывает, как живут животные на островах, в горах, джунглях, пустынях, на равнинах и в городах — снято с помощью новейших технологий, которые позволили увидеть мир дикой природы ближе, чем когда-либо.',
    cast: [{ name: 'Дэвид Аттенборо', character: 'рассказчик' }],
  },
}

async function newPage(browser: Browser, baseURL: string, v: (typeof VIEWPORTS)[number]) {
  const ctx = await browser.newContext({
    baseURL,
    viewport: { width: v.width, height: v.height },
    deviceScaleFactor: v.deviceScaleFactor,
    isMobile: v.isMobile,
    hasTouch: v.isMobile,
    colorScheme: 'dark',
    locale: 'ru-RU',
    reducedMotion: 'reduce',
  })
  const page = await ctx.newPage()
  await page.route(/\/api\/items\/\d+$/, async (route) => {
    const res = await route.fetch()
    const json = (await res.json()) as { title: string; files?: { resolution?: string }[] }
    const extra = ENRICH[json.title] ?? {}
    const files = (json.files ?? []).map((f) => ({ ...f, resolution: '4K' }))
    await route.fulfill({ response: res, json: { ...json, ...extra, files } })
  })
  await page.route(/\/api\/images\/[a-z0-9-]+\.(png|jpg)$/, async (route) => {
    const name = path.basename(new URL(route.request().url()).pathname)
    await route.fulfill({ body: await readFile(path.join(IMAGES, name)), contentType: name.endsWith('.png') ? 'image/png' : 'image/jpeg' })
  })
  return page
}

async function idOf(page: Page, kind: string, title: string) {
  await expect
    .poll(async () => ((await (await page.request.get(`/api/items?kind=${kind}`)).json()) as { total: number }).total, { timeout: 20_000 })
    .toBeGreaterThan(0)
  const r = (await (await page.request.get(`/api/items?kind=${kind}`)).json()) as { items: { id: number; title: string }[] }
  return r.items.find((c) => c.title === title)!.id
}

test('item page and player screenshots', async ({ browser, baseURL, page: first }) => {
  await setupAdmin(first.request)
  const cookies = await first.context().cookies()
  for (const v of VIEWPORTS) {
    const page = await newPage(browser, baseURL!, v)
    await page.context().addCookies(cookies)
    const movie = await idOf(page, 'movie', PLAYBACK.direct.title)
    const series = await idOf(page, 'series', PLAYBACK.series.title)

    await page.goto('/movies')
    await expect(page.getByRole('heading', { name: 'Фильмы', level: 1 })).toBeVisible()
    await page.getByRole('radio', { name: 'По названию' }).click()
    await page.waitForLoadState('networkidle')
    await shot(page, `movies-${v.tag}`)

    await page.goto(`/item/${movie}`)
    await expect(page.getByRole('heading', { name: 'Интерстеллар', level: 1 })).toBeVisible()
    await page.waitForLoadState('networkidle')
    await page.waitForTimeout(400)
    await shot(page, `detail-movie-${v.tag}`)
    if (v.isMobile) {
      await page.mouse.wheel(0, 380)
      await page.waitForTimeout(400)
      await shot(page, 'detail-movie-actions-phone')
    }
    await page.mouse.wheel(0, 900)
    await page.waitForTimeout(400)
    await shot(page, `detail-movie-below-${v.tag}`)

    await page.goto(`/item/${series}`)
    await expect(page.getByRole('region', { name: 'Серии' })).toBeVisible()
    await page.waitForLoadState('networkidle')
    await page.waitForTimeout(600)
    await shot(page, `detail-series-${v.tag}`)
    await page.mouse.wheel(0, 700)
    await page.waitForTimeout(400)
    await shot(page, `detail-series-below-${v.tag}`)

    // Player: playing with subtitles, then paused with the controls up.
    await page.goto(`/watch/${movie}`)
    await expect.poll(() => page.locator('video').evaluate((el: HTMLVideoElement) => el.currentTime), { timeout: 20_000 }).toBeGreaterThan(1)
    await page.getByRole('button', { name: 'Аудио и субтитры' }).click()
    await page.getByRole('menu', { name: 'Аудио и субтитры' }).getByRole('menuitemradio', { name: /Русский/ }).click()
    await page.getByRole('button', { name: 'Аудио и субтитры' }).click()
    await page.waitForTimeout(400)
    await shot(page, `player-tracks-${v.tag}`)
    await page.keyboard.press('Escape')
    await page.locator('video').evaluate((el: HTMLVideoElement) => el.pause())
    if (!v.isMobile) await page.mouse.move(700, 450)
    await page.waitForTimeout(400)
    await shot(page, `player-${v.tag}`)
    await page.close()
  }
})

test('up next screenshot', async ({ page }) => {
  await setupAdmin(page.request)
  const series = await idOf(page, 'series', PLAYBACK.series.title)
  const d = (await (await page.request.get(`/api/items/${series}`)).json()) as { seasons: { episodes: { id: number }[] }[] }
  await page.emulateMedia({ reducedMotion: 'reduce' })
  await page.goto(`/watch/${d.seasons[0]!.episodes[0]!.id}?t=6`)
  await expect(page.getByRole('complementary', { name: 'Следующая серия' })).toBeVisible({ timeout: 20_000 })
  await page.mouse.move(600, 400)
  await page.waitForTimeout(500)
  await shot(page, 'player-upnext-desktop')
})

test('subtitle search screenshots', async ({ browser, baseURL, page: first }) => {
  await setupAdmin(first.request)
  const cookies = await first.context().cookies()
  const results = [
    { subtitleId: 101, lang: 'ru', release: 'Interstellar.2014.2160p.UHD.BluRay.x265-GRP', downloads: 48213, hashMatch: true },
    { subtitleId: 102, lang: 'ru', release: 'Interstellar.2014.1080p.BluRay.x264-GRP2', downloads: 21907, hashMatch: false },
    { subtitleId: 103, lang: 'ru', release: 'Интерстеллар (2014) BDRip — перевод Пифагор', downloads: 9344, hashMatch: false },
    { subtitleId: 104, lang: 'ru', release: 'Interstellar.2014.IMAX.WEB-DL.1080p', downloads: 3120, hashMatch: false },
  ]
  for (const v of VIEWPORTS) {
    const page = await newPage(browser, baseURL!, v)
    await page.context().addCookies(cookies)
    // the e2e server has no OpenSubtitles key: the results are a fixture
    await page.route(/\/api\/subtitles\/\d+\/search\?/, (r) => r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ results }) }))
    const movie = await idOf(page, 'movie', PLAYBACK.direct.title)
    await page.goto(`/watch/${movie}`)
    await expect.poll(() => page.locator('video').evaluate((el: HTMLVideoElement) => el.currentTime), { timeout: 20_000 }).toBeGreaterThan(1)
    await page.locator('video').evaluate((el: HTMLVideoElement) => el.pause())
    await page.getByRole('button', { name: 'Аудио и субтитры' }).click()
    await page.getByRole('menuitem', { name: 'Найти субтитры…' }).click()
    await expect(page.getByRole('list', { name: 'Найденные субтитры' })).toBeVisible()
    await page.waitForTimeout(400)
    await shot(page, `player-subtitle-search-${v.tag}`)
    await page.close()
  }
})
