// «Смотреть ▾» against bin/vault: a library film with two files, its versions
// menu and the download to the device (another build's states, a title not on
// the server and the search cards: watch-buttons.full.spec.ts). The assertions always run; with
//   SCREENSHOTS=1 npx playwright test e2e/watch-buttons.spec.ts --project chromium
// the states are also saved to docs/screenshots/watch-buttons-*.png
// (SHOTS_DIR and SHOTS_PREFIX redirect them, e.g. for a before/after check).
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Browser, Locator, Page } from '@playwright/test'
import { expect, LIBRARY_MOVIE, scanLibrary, setupAdmin, test } from './vault'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const OUT = process.env.SHOTS_DIR ?? path.join(HERE, '..', '..', 'docs', 'screenshots')
const IMAGES = path.join(HERE, 'fixtures', 'images')
const SHOOT = Boolean(process.env.SCREENSHOTS)

test.use({ library: 'sample', fakes: true })
test.describe.configure({ timeout: 90_000 })

const VIEWPORTS = {
  desktop: { width: 1440, height: 900, deviceScaleFactor: 2, isMobile: false },
  phone: { width: 390, height: 844, deviceScaleFactor: 2, isMobile: true },
} as const
type Where = keyof typeof VIEWPORTS

async function newPage(browser: Browser, baseURL: string, where: Where, extra: { contrast?: 'more' } = {}) {
  const v = VIEWPORTS[where]
  const ctx = await browser.newContext({
    baseURL,
    viewport: { width: v.width, height: v.height },
    deviceScaleFactor: v.deviceScaleFactor,
    isMobile: v.isMobile,
    hasTouch: v.isMobile,
    locale: 'ru-RU',
    colorScheme: 'dark',
    contrast: extra.contrast,
    reducedMotion: 'reduce', // stable frames
    serviceWorkers: 'block',
  })
  const page = await ctx.newPage()
  await page.route('**/e2e-art/*', async (route) => {
    const name = path.basename(new URL(route.request().url()).pathname)
    await route.fulfill({ status: 200, contentType: 'image/jpeg', body: await readFile(path.join(IMAGES, name)) })
  })
  return page
}

async function settle(page: Page) {
  await expect.poll(() => page.evaluate(() => [...document.querySelectorAll('img')].every((i) => i.complete)), { timeout: 15_000 }).toBe(true)
  await page.waitForTimeout(400)
}

/** Saves a PNG when shooting: the whole viewport (CSS pixels), or the box around `around` with `pad` CSS px (always at the device's full resolution). */
async function shot(page: Page, name: string, around?: Locator[], pad = 24) {
  if (!SHOOT) return
  await settle(page)
  const file = path.join(OUT, `${process.env.SHOTS_PREFIX ?? 'watch-buttons'}-${name}.png`)
  if (!around) return void (await page.screenshot({ path: file, type: 'png', scale: page.viewportSize()!.width > 600 ? 'css' : 'device' }))
  const boxes = (await Promise.all(around.map((l) => l.boundingBox()))).filter((b) => b !== null)
  const x = Math.max(0, Math.min(...boxes.map((b) => b.x)) - pad)
  const y = Math.max(0, Math.min(...boxes.map((b) => b.y)) - pad)
  const vp = page.viewportSize()!
  const right = Math.min(vp.width, Math.max(...boxes.map((b) => b.x + b.width)) + pad)
  const bottom = Math.min(vp.height, Math.max(...boxes.map((b) => b.y + b.height)) + pad)
  await page.screenshot({ path: file, type: 'png', clip: { x, y, width: right - x, height: bottom - y } })
}

const MB = 1 << 20
const track = (index: number, lang: string, codec = 'ac3') => ({ index, codec, lang, title: '', channels: 6, default: index === 1 })
const sub = (index: number, lang: string) => ({ index, codec: 'subrip', lang, title: '', forced: false, default: false, external: false })

/** The sample film as it looks with two files: 2160p (RU, EN + subtitles) and 1080p. */
async function twoFiles(page: Page) {
  await page.route(/\/api\/items\/\d+$/, async (r) => {
    const res = await r.fetch()
    const item = (await res.json()) as { files: Record<string, unknown>[] } & Record<string, unknown>
    const base = item.files[0]!
    const files = [
      { ...base, id: Number(base.id), name: 'Interstellar.2014.2160p.mkv', size: 4200 * MB, resolution: '2160p', durationMs: 169 * 60_000, audio: [track(1, 'rus'), track(2, 'eng')], subs: [sub(3, 'eng'), sub(4, 'rus')] },
      { ...base, id: Number(base.id) + 1000, name: 'Interstellar.2014.1080p.mkv', size: 1800 * MB, resolution: '1080p', durationMs: 169 * 60_000, audio: [track(1, 'rus')], subs: [] },
    ]
    await r.fulfill({ response: res, json: { ...item, files } })
  })
}

const movieId = async (page: Page) => {
  await scanLibrary(page.request)
  const list = (await (await page.request.get('/api/items?kind=movie')).json()) as { items: { id: number; title: string }[] }
  return list.items.find((i) => i.title === LIBRARY_MOVIE.title)!.id
}

for (const where of ['desktop', 'phone'] as const) {
  test.describe(where, () => {
    test('library film: «Смотреть ▾», the versions menu and the download to the device', async ({ browser, vaultURL }) => {
      const page = await newPage(browser, vaultURL, where)
      await setupAdmin(page.request)
      const id = await movieId(page)
      await twoFiles(page)
      await page.goto(`/item/${id}`)
      await expect(page.getByRole('heading', { name: LIBRARY_MOVIE.title })).toBeVisible()
      const watch = page.getByRole('button', { name: /^Смотреть/ }).first()
      await expect(watch).toBeVisible()
      const watchlist = page.getByRole('button', { name: 'Хочу посмотреть' })
      const other = page.getByRole('button', { name: 'Ещё', exact: true })
      const toDevice = page.getByRole('button', { name: 'Загрузить', exact: true })
      await expect(toDevice).toBeVisible()
      await shot(page, `library-row-${where}`, [watch, toDevice, watchlist, other], 16)

      await toDevice.click()
      const menu = page.getByRole('menu', { name: 'Скачать на это устройство' })
      await expect(menu.getByRole('menuitem')).toHaveCount(2)
      if (where === 'desktop') await shot(page, `library-device-menu-${where}`, [watch, toDevice, menu], 16)
      await page.keyboard.press('Escape')

      await other.click()
      await expect(page.getByRole('menuitem', { name: /Смотреть в 1080p/ })).toBeVisible()
      await expect(page.getByRole('menuitem', { name: /Смотреть в 2160p/ })).toContainText('RU, EN · Субтитры: EN, RU')
      if (where === 'desktop') await shot(page, `library-versions-${where}`, [watch, other, page.getByRole('menu')], 16)
      await page.context().close()
    })
  })
}
