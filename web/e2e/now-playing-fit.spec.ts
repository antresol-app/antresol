// «Сейчас играет» fits the window at any size: a 1280×640 Chrome window on
// Windows cut the bottom row (lyrics, sound, queue) in half, because the cover
// was sized from the viewport's height alone (46vh) and the rows under it did
// not count. Every control must lie inside the viewport, with the cover shown
// and with lyrics open. The rows' height is an estimate in CSS (--np-rows), so
// the test also makes them taller than it (a longer font, «Playing on …») and
// takes the room away altogether: the cover gives the height up, and when even
// its minimum does not fit the body scrolls to the last row.
// The sizes are the owner's laptop window (1280×610 CSS px of page), the
// common laptop and desktop windows (1440×700, 1366×768, the 2560×1440
// display at 1x), a phone and a phone on its side. A track, an audiobook,
// lyrics and the queue open: all of them fit each one.
// Set NP_SHOTS=<dir> to keep a screenshot of each size.
import path from 'node:path'
import type { Browser, Locator, Page } from '@playwright/test'
import { COMICS_AUDIO } from './comics-audio-media'
import { MUSIC } from './music-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

test.use({ library: 'all' })
test.describe.configure({ timeout: 120_000 })

const SIZES = [
  { width: 1280, height: 501, phone: false },
  { width: 1280, height: 610, phone: false },
  { width: 1280, height: 640, phone: false },
  { width: 1280, height: 650, phone: false },
  { width: 1280, height: 720, phone: false },
  { width: 1366, height: 650, phone: false },
  { width: 1366, height: 768, phone: false },
  { width: 1440, height: 700, phone: false },
  { width: 1440, height: 900, phone: false },
  { width: 1920, height: 1080, phone: false },
  { width: 2560, height: 1440, phone: false },
  { width: 390, height: 844, phone: true },
  { width: 390, height: 667, phone: true },
  { width: 844, height: 390, phone: true },
] as const

async function sizedPage(browser: Browser, baseURL: string, v: (typeof SIZES)[number]) {
  const ctx = await browser.newContext({
    baseURL,
    viewport: { width: v.width, height: v.height },
    isMobile: v.phone,
    hasTouch: v.phone,
    colorScheme: 'dark',
    locale: 'ru-RU',
    reducedMotion: 'reduce',
  })
  return ctx.newPage()
}

/** Every button and the volume slider of the player lie fully inside the viewport. */
async function expectAllInside(full: Locator, v: { width: number; height: number }) {
  const controls = full.locator('button:visible, input[type="range"]:visible')
  const n = await controls.count()
  expect(n).toBeGreaterThan(6)
  for (let i = 0; i < n; i++) {
    const c = controls.nth(i)
    const box = (await c.boundingBox())!
    const name = (await c.getAttribute('aria-label')) ?? (await c.textContent()) ?? `#${i}`
    const where = `${name} at ${v.width}×${v.height}: ${JSON.stringify(box)}`
    expect.soft(box.x, where).toBeGreaterThanOrEqual(0)
    expect.soft(box.y, where).toBeGreaterThanOrEqual(0)
    expect.soft(box.x + box.width, where).toBeLessThanOrEqual(v.width + 0.5)
    expect.soft(box.y + box.height, where).toBeLessThanOrEqual(v.height + 0.5)
  }
}

/** Rows taller than the CSS estimate: the title block grows by `px` (a wider font, a «Playing on …» line). Returns the undo. */
async function growRows(page: Page, px: number) {
  const style = await page.addStyleTag({ content: `[role="dialog"] [class*="_meta_"] { padding-bottom: ${px}px }` })
  await page.waitForTimeout(150)
  return () => style.evaluate((el) => el.parentNode?.removeChild(el))
}

test('the full player fits the viewport at every size', async ({ browser, browserName, baseURL, page: first }) => {
  test.skip(browserName !== 'chromium', 'layout only; one engine is enough')
  await setupAdmin(first.request)
  await scanLibrary(first.request, (s) => s.albums >= 2)
  const cookies = await first.context().cookies()
  const albums = (await (await first.request.get('/api/music/albums?sort=title')).json()) as { items: { id: number; title: string }[] }
  const albumId = albums.items.find((a) => a.title === MUSIC.album)!.id

  for (const v of SIZES) {
    const page = await sizedPage(browser, baseURL!, v)
    await page.context().addCookies(cookies)
    await page.goto(`/music/album/${albumId}`)
    await page.getByRole('button', { name: 'Слушать' }).click()
    const mini = page.getByRole('complementary', { name: 'Сейчас играет' })
    await mini.getByRole('button', { name: /Открыть плеер/ }).click()
    const full = page.getByRole('dialog', { name: 'Сейчас играет' })
    await expect(full).toBeVisible()
    await expect(full.getByRole('button', { name: 'Очередь' })).toBeVisible()
    await page.waitForTimeout(500)

    const shot = (tag: string) =>
      process.env.NP_SHOTS ? page.screenshot({ path: path.join(process.env.NP_SHOTS, `${v.width}x${v.height}${tag}.png`) }) : null
    await shot('')
    await expectAllInside(full, v)
    // the cover is still there and not squeezed to nothing
    const art = full.locator('img:not([aria-hidden] img)').first()
    if (await art.isVisible()) expect((await art.boundingBox())!.width).toBeGreaterThan(100)

    if (!v.phone) {
      // rows 30 px taller than the estimate: the cover is smaller, the rows still all there
      const before = (await art.boundingBox())!.width
      const undo = await growRows(page, 30)
      await shot('-tall-rows')
      await expectAllInside(full, v)
      expect((await art.boundingBox())!.width).toBeLessThan(before + 0.5)
      await undo()
    }

    await full.getByRole('button', { name: 'Текст', exact: true }).click()
    await expect(full.getByRole('button', { name: 'Текст', exact: true })).toHaveAttribute('aria-pressed', 'true')
    await page.waitForTimeout(400)
    await shot('-lyrics')
    await expectAllInside(full, v)
    await full.getByRole('button', { name: 'Очередь', exact: true }).click()
    await expect(full.getByRole('button', { name: 'Очередь', exact: true })).toHaveAttribute('aria-pressed', 'true')
    await page.waitForTimeout(400)
    await shot('-queue')
    await expectAllInside(full, v)
    await page.context().close()
  }
})

test('an audiobook in the full player fits the viewport at every size', async ({ browser, browserName, baseURL, page: first }) => {
  test.skip(browserName !== 'chromium', 'layout only; one engine is enough')
  await setupAdmin(first.request)
  await scanLibrary(first.request, (s) => s.audiobooks >= 1)
  const cookies = await first.context().cookies()
  const books = (await (await first.request.get('/api/books?kind=audiobook')).json()) as { items: { id: number; title: string }[] }
  const book = books.items.find((b) => b.title === COMICS_AUDIO.audiobook)!.id

  for (const v of SIZES) {
    const page = await sizedPage(browser, baseURL!, v)
    await page.context().addCookies(cookies)
    await page.goto(`/books/audiobook/${book}`)
    await page.getByRole('button', { name: /^(Слушать|Продолжить)/ }).first().click()
    await page.getByRole('complementary', { name: 'Сейчас играет' }).getByRole('button', { name: /Открыть плеер/ }).click()
    const full = page.getByRole('dialog', { name: 'Сейчас играет' })
    await expect(full).toBeVisible()
    await expect(full.getByRole('button', { name: 'Таймер сна' })).toBeVisible()
    await page.waitForTimeout(500)
    if (process.env.NP_SHOTS) await page.screenshot({ path: path.join(process.env.NP_SHOTS, `${v.width}x${v.height}-audiobook.png`) })
    await expectAllInside(full, v)
    await page.context().close()
  }
})

test('with no room left the full player scrolls to its last row instead of cutting it', async ({ browser, baseURL, page: first }) => {
  await setupAdmin(first.request)
  await scanLibrary(first.request, (s) => s.albums >= 2)
  const cookies = await first.context().cookies()
  const albums = (await (await first.request.get('/api/music/albums?sort=title')).json()) as { items: { id: number; title: string }[] }
  const v = { width: 1280, height: 650, phone: false } as const
  const page = await sizedPage(browser, baseURL!, v)
  await page.context().addCookies(cookies)
  await page.goto(`/music/album/${albums.items.find((a) => a.title === MUSIC.album)!.id}`)
  await page.getByRole('button', { name: 'Слушать' }).click()
  await page.getByRole('complementary', { name: 'Сейчас играет' }).getByRole('button', { name: /Открыть плеер/ }).click()
  const full = page.getByRole('dialog', { name: 'Сейчас играет' })
  await expect(full.getByRole('button', { name: 'Очередь' })).toBeVisible()

  await growRows(page, 400)
  await page.waitForTimeout(300)
  if (process.env.NP_SHOTS) await page.screenshot({ path: path.join(process.env.NP_SHOTS, 'no-room.png') })
  // the cover is at its minimum, not gone
  const art = full.locator('img:not([aria-hidden] img)').first()
  expect((await art.boundingBox())!.width).toBeGreaterThan(90)
  // the body scrolls; the last row comes into view whole
  const queue = full.getByRole('button', { name: 'Очередь' })
  await queue.scrollIntoViewIfNeeded()
  const box = (await queue.boundingBox())!
  expect(box.y).toBeGreaterThanOrEqual(0)
  expect(box.y + box.height).toBeLessThanOrEqual(v.height + 0.5)
  // and the first rows are reachable at the top: the cover is not pushed out of the window above
  await full.evaluate((root) => root.querySelectorAll('*').forEach((e) => (e.scrollTop = 0)))
  expect((await art.boundingBox())!.y).toBeGreaterThanOrEqual(0)
  await page.context().close()
})
