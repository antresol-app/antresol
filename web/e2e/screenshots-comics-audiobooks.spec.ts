// Reference screenshots of a manga series, the comic reader (pages right to
// left, the ribbon), «Аудиокниги», an audiobook page with chapters and the
// audiobook in «Сейчас играет» (with the sleep timer menu), against bin/vault
// with the generated library (comics-audio-media.ts). Opt-in:
//   SCREENSHOTS=1 npx playwright test e2e/screenshots-comics-audiobooks.spec.ts --project chromium
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Browser, Page } from '@playwright/test'
import { COMICS_AUDIO } from './comics-audio-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const OUT = path.join(HERE, '..', '..', 'docs', 'screenshots')
const shot = (page: Page, name: string) => page.screenshot({ path: path.join(OUT, `books-${name}.jpg`), type: 'jpeg', quality: 85 })

test.skip(!process.env.SCREENSHOTS, 'set SCREENSHOTS=1 to refresh docs/screenshots')
test.use({ library: 'books' })
test.describe.configure({ timeout: 240_000 })

const VIEWPORTS = [
  { tag: 'desktop', width: 1440, height: 900, deviceScaleFactor: 2, isMobile: false },
  { tag: 'phone', width: 390, height: 844, deviceScaleFactor: 3, isMobile: true },
] as const

/** Artwork-less audiobooks beside the real one, so the grid shows its density. */
const FILLERS = [
  ['Мастер и Маргарита', 'Михаил Булгаков'],
  ['Пикник на обочине', 'Аркадий и Борис Стругацкие'],
  ['Шерлок Холмс', 'Артур Конан Дойл'],
  ['Сто лет одиночества', 'Габриэль Гарсиа Маркес'],
  ['Дюна', 'Фрэнк Герберт'],
].map(([title, subtitle], i) => ({ id: 950 + i, kind: 'audiobook', title, subtitle, poster: '', backdrop: '', logo: '', progress: 0, onServer: true }))

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
  await page.route(/\/api\/books\?kind=audiobook/, async (route) => {
    const res = await route.fetch()
    const json = (await res.json()) as { items: unknown[]; total: number }
    json.items = [...json.items, ...FILLERS]
    json.total = json.items.length
    await route.fulfill({ response: res, json })
  })
  return page
}

const settle = async (page: Page, ms = 500) => {
  await page.waitForLoadState('networkidle')
  await page.waitForTimeout(ms)
}

test('comics and audiobooks screenshots', async ({ browser, baseURL, page: first }) => {
  await setupAdmin(first.request)
  await scanLibrary(first.request, (s) => s.comics >= 2 && s.audiobooks >= 1)
  const cookies = await first.context().cookies()
  const get = async <T,>(url: string) => (await (await first.request.get(url)).json()) as T
  const comic = (await get<{ items: { id: number; title: string }[] }>('/api/books?kind=comic')).items.find((b) => b.title === COMICS_AUDIO.comic)!.id
  const book = (await get<{ items: { id: number; title: string }[] }>('/api/books?kind=audiobook')).items.find((b) => b.title === COMICS_AUDIO.audiobook)!.id
  const vols = (await get<{ volumes: { id: number }[] }>(`/api/books/${comic}`)).volumes
  const vol1 = vols[0]!.id
  // «Том 1» half read; the audiobook 25 s in
  const progress = async () => {
    expect((await first.request.put(`/api/progress/${vol1}`, { data: { locator: 'page:3', fraction: 0.5 } })).ok()).toBeTruthy()
    expect((await first.request.put(`/api/progress/${book}`, { data: { positionMs: 25_000, durationMs: 60_000 } })).ok()).toBeTruthy()
  }

  for (const v of VIEWPORTS) {
    await progress()
    const page = await newPage(browser, baseURL!, v)
    await page.context().addCookies(cookies)

    await page.goto(`/books/comic/${comic}`)
    await expect(page.getByRole('heading', { level: 1, name: COMICS_AUDIO.comic })).toBeVisible()
    await settle(page)
    if (!v.isMobile) await page.mouse.move(v.width - 10, 10)
    await shot(page, `comic-series-${v.tag}`)

    // the reader at page 3, panels up as on open
    await page.getByRole('link', { name: 'Продолжить: Том 1' }).click()
    await expect(page.getByTestId('comic-reader')).toHaveAttribute('data-page', '3')
    await settle(page)
    await shot(page, `comic-reader-${v.tag}`)
    await page.getByRole('button', { name: 'Вид' }).click()
    await page.getByRole('menuitemradio', { name: /Лента/ }).click()
    await expect(page.getByTestId('comic-reader')).toHaveAttribute('data-view', 'ribbon')
    await settle(page)
    await page.getByTestId('comic-ribbon').evaluate((el) => el.scrollBy(0, el.clientHeight * 0.6))
    await page.waitForTimeout(400)
    // scrolling hid the panels: a tap brings them back
    await page.mouse.click(v.width / 2, v.height / 2)
    await expect(page.getByTestId('comic-reader')).not.toHaveAttribute('data-hidden', '')
    await page.waitForTimeout(400)
    await shot(page, `comic-ribbon-${v.tag}`)
    // back to pages for the next viewport
    await page.evaluate(() => Object.keys(localStorage).forEach((k) => k.startsWith('vault.comic.') && localStorage.removeItem(k)))

    await page.goto('/books/all/audiobook')
    await settle(page)
    await shot(page, `audiobooks-${v.tag}`)

    await page.goto(`/books/audiobook/${book}`)
    await expect(page.getByRole('heading', { level: 1, name: COMICS_AUDIO.audiobook })).toBeVisible()
    await settle(page)
    await shot(page, `audiobook-${v.tag}`)

    await page.getByRole('button', { name: /^Продолжить · осталось/ }).click()
    await expect(page.getByTestId('mini-player')).toContainText('Глава 2')
    await page.waitForTimeout(800)
    await page.getByRole('button', { name: /Открыть плеер/ }).click()
    const np = page.getByRole('dialog', { name: 'Сейчас играет' })
    await expect(np).toBeVisible()
    await page.waitForTimeout(800)
    await shot(page, `audiobook-now-playing-${v.tag}`)
    await np.getByRole('button', { name: 'Таймер сна' }).click()
    await expect(page.getByRole('menu', { name: 'Таймер сна' })).toBeVisible()
    await page.waitForTimeout(400)
    await shot(page, `audiobook-sleep-${v.tag}`)
    await page.context().close()
  }
})
