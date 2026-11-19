// «Сейчас играет» fits the window at any size: a 1280×640 Chrome window on
// Windows cut the bottom row (lyrics, sound, queue) in half, because the cover
// was sized from the viewport's height alone (46vh) and the rows under it did
// not count. Every control must lie inside the viewport, with the cover shown
// and with lyrics open. Set NP_SHOTS=<dir> to keep a screenshot of each size.
import path from 'node:path'
import type { Browser, Locator } from '@playwright/test'
import { MUSIC } from './music-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

test.use({ library: 'music' })
test.describe.configure({ timeout: 120_000 })

const SIZES = [
  { width: 1280, height: 640, phone: false },
  { width: 1366, height: 650, phone: false },
  { width: 1440, height: 900, phone: false },
  { width: 1920, height: 1080, phone: false },
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

    await full.getByRole('button', { name: 'Текст', exact: true }).click()
    await expect(full.getByRole('button', { name: 'Текст', exact: true })).toHaveAttribute('aria-pressed', 'true')
    await page.waitForTimeout(400)
    await shot('-lyrics')
    await expectAllInside(full, v)
    await page.context().close()
  }
})
