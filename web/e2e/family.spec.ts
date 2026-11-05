// «Семья» against bin/vault: four profiles watch the same series and film,
// the admin's series page shows where everyone is (folded, then unfolded),
// «Продолжить с места» starts the player at Мама's time in the admin's own
// progress, a kid sees nobody's, and Главная has «Семья смотрит». With
//   SCREENSHOTS=1 npx playwright test e2e/family.spec.ts --project chromium
// the states are saved to docs/screenshots/family-*.png (SHOTS_DIR redirects).
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { request as playwrightRequest, type Browser, type Page } from '@playwright/test'
import { PLAYBACK } from './media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const OUT = process.env.SHOTS_DIR ?? path.join(HERE, '..', '..', 'docs', 'screenshots')
const SHOOT = Boolean(process.env.SCREENSHOTS)

test.use({ library: 'playback' })
test.describe.configure({ timeout: 120_000 })

const MIN = 60_000
const VIEWPORTS = {
  desktop: { width: 1440, height: 900, isMobile: false },
  phone: { width: 390, height: 844, isMobile: true },
} as const

async function newPage(browser: Browser, baseURL: string, where: keyof typeof VIEWPORTS) {
  const v = VIEWPORTS[where]
  const ctx = await browser.newContext({
    baseURL,
    viewport: { width: v.width, height: v.height },
    deviceScaleFactor: 2,
    isMobile: v.isMobile,
    hasTouch: v.isMobile,
    locale: 'ru-RU',
    colorScheme: 'dark',
    reducedMotion: 'reduce',
    serviceWorkers: 'block',
  })
  return ctx.newPage()
}

async function shot(page: Page, name: string) {
  if (!SHOOT) return
  await expect.poll(() => page.evaluate(() => [...document.querySelectorAll('img')].every((i) => i.complete)), { timeout: 15_000 }).toBe(true)
  await page.waitForTimeout(400)
  await page.screenshot({ path: path.join(OUT, `family-${name}.png`), type: 'png', scale: 'css' })
}

/** A profile signed in on its own (another device): saves its positions. */
async function asProfile(baseURL: string, userId: number) {
  const ctx = await playwrightRequest.newContext({ baseURL })
  expect((await ctx.post('/api/login', { data: { userId } })).ok()).toBeTruthy()
  return {
    save: async (itemId: number, positionMs: number, durationMs: number) => {
      const r = await ctx.put(`/api/progress/${itemId}`, { data: { positionMs, durationMs } })
      expect(r.status(), await r.text()).toBe(204)
    },
    dispose: () => ctx.dispose(),
  }
}

interface Ids {
  series: number
  episodes: number[]
  movie: number
  kid: number
}

/** The library, three more profiles and their places in it. */
async function family(page: Page, baseURL: string): Promise<Ids> {
  await setupAdmin(page.request)
  await scanLibrary(page.request, (s) => s.episodes >= 3 && s.movies > 0)
  const list = (await (await page.request.get('/api/items?kind=series')).json()) as { items: { id: number; title: string }[] }
  const series = list.items.find((i) => i.title === PLAYBACK.series.title)!.id
  const detail = (await (await page.request.get(`/api/items/${series}`)).json()) as { seasons: { episodes: { id: number }[] }[] }
  const episodes = detail.seasons[0]!.episodes.map((e) => e.id)
  const movies = (await (await page.request.get('/api/items?kind=movie')).json()) as { items: { id: number; title: string }[] }
  const movie = movies.items.find((i) => i.title === PLAYBACK.direct.title)!.id
  const profile = async (data: Record<string, unknown>) => {
    const r = await page.request.post('/api/users', { data })
    expect(r.status(), await r.text()).toBe(201)
    return ((await r.json()) as { id: number }).id
  }
  const mom = await profile({ name: 'Мама', avatar: 'grad-3' })
  const son = await profile({ name: 'Петя', avatar: 'grad-4' })
  const gran = await profile({ name: 'Бабушка', avatar: 'grad-5' })
  const kid = await profile({ name: 'Вася', avatar: 'grad-2', isKid: true, maxRating: '12+' })

  // Петя finished E1; Бабушка has seen all; Мама is in the middle of E2 at 17 min
  const s = await asProfile(baseURL, son)
  await s.save(episodes[0]!, 45 * MIN, 45 * MIN)
  await s.dispose()
  const g = await asProfile(baseURL, gran)
  for (const ep of episodes) await g.save(ep, 45 * MIN, 45 * MIN)
  await g.dispose()
  const k = await asProfile(baseURL, kid) // the series is unrated: a kid may only open the 12+ film
  await k.save(movie, 5 * MIN, 110 * MIN)
  await k.dispose()
  const m = await asProfile(baseURL, mom)
  await m.save(movie, 34 * MIN, 110 * MIN)
  await m.save(episodes[0]!, 44 * MIN, 45 * MIN)
  await m.save(episodes[1]!, 17 * MIN, 45 * MIN)
  await m.dispose()
  return { series, episodes, movie, kid }
}

for (const where of ['desktop', 'phone'] as const) {
  test(`${where}: where everyone is, and continuing from Мама's place`, async ({ browser, vaultURL }) => {
    const page = await newPage(browser, vaultURL, where)
    const ids = await family(page, vaultURL)

    await page.goto(`/item/${ids.series}`)
    const fold = page.getByRole('button', { name: /^Семья/ })
    await expect(fold).toBeVisible()
    await expect(fold).toContainText('Мама · С1 Э2 · 17 мин') // the most recent
    await expect(fold).toContainText('ещё 2')
    await shot(page, `series-${where}`)

    await fold.click()
    const list = page.locator('section[aria-label="Семья"]')
    await expect(list.getByText('Далее: С1 Э2')).toBeVisible()
    await expect(list.getByText('Всё просмотрено')).toBeVisible()
    await list.scrollIntoViewIfNeeded()
    await shot(page, `series-open-${where}`)
    await page.keyboard.press('Escape')
    await expect(list.getByText('Всё просмотрено')).toBeHidden()
    // who is on which episode: their faces on its tile
    const episodes = page.getByRole('list', { name: 'Сезон 1' })
    await expect(episodes.locator('[title="Мама, Петя"]')).toBeVisible()
    await episodes.scrollIntoViewIfNeeded()
    await shot(page, `episodes-${where}`)

    if (where === 'phone') return void (await page.context().close())

    // the player starts at her 17:00, in the admin's own progress
    await fold.click()
    await list.getByRole('button', { name: /С1 Э2 с 17:00 — с места профиля «Мама»/ }).click()
    await expect(page).toHaveURL(new RegExp(`/watch/${ids.episodes[1]}\\?t=1020$`))
    const mine = (await (await page.request.get(`/api/progress/${ids.episodes[1]}`)).json()) as { positionMs: number }
    expect(mine.positionMs).toBe(17 * MIN)

    // the film page, and Главная's «Семья смотрит»
    await page.goto(`/item/${ids.movie}`)
    await page.getByRole('button', { name: /^Семья/ }).click()
    await expect(page.locator('section[aria-label="Семья"] li').getByText('34 мин из 1 ч 50 мин')).toBeVisible()
    await shot(page, `movie-open-${where}`)
    await page.goto('/')
    const shelf = page.getByRole('region', { name: /Семья смотрит/ })
    await expect(shelf).toBeVisible()
    await shelf.scrollIntoViewIfNeeded()
    await shot(page, `home-${where}`)

    // a kid sees nobody's progress
    await page.request.post('/api/logout')
    expect((await page.request.post('/api/login', { data: { userId: ids.kid } })).ok()).toBeTruthy()
    await page.goto(`/item/${ids.movie}`)
    await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
    await expect(page.getByRole('button', { name: /^Смотреть/ }).first()).toBeVisible()
    await expect(page.getByRole('button', { name: /^Семья/ })).toHaveCount(0)
    await page.context().close()
  })
}
