// «Семья» on the player's timeline against bin/vault: Мама, Папа and Бабушка
// are somewhere in the film, the admin watches from 0:08. Their faces stand
// on the scrubber (Папа and Бабушка, a second apart, fan out), a hover names
// the place, a click jumps there and «Вернуться к 0:08» takes the admin back;
// the jump alone never becomes the admin's progress. With
//   SCREENSHOTS=1 npx playwright test e2e/family-timeline.spec.ts --project chromium
// the states are saved to docs/screenshots/family-timeline-*.png (SHOTS_DIR redirects).
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

/** The whole viewport (CSS pixels), and the controls up close at the device's resolution. */
async function shot(page: Page, name: string) {
  if (!SHOOT) return
  await page.waitForTimeout(400)
  await page.screenshot({ path: path.join(OUT, `family-timeline-${name}.png`), type: 'png', scale: 'css' })
  const vp = page.viewportSize()!
  const box = (await page.locator('footer').boundingBox())!
  const top = Math.max(0, box.y - 60)
  await page.screenshot({ path: path.join(OUT, `family-timeline-${name}-closeup.png`), type: 'png', clip: { x: 0, y: top, width: vp.width, height: vp.height - top } })
}

/** The film, and three more profiles somewhere in it. */
async function family(page: Page, baseURL: string): Promise<number> {
  await setupAdmin(page.request)
  await scanLibrary(page.request, (s) => s.movies > 0)
  const movies = (await (await page.request.get('/api/items?kind=movie')).json()) as { items: { id: number; title: string }[] }
  const movie = movies.items.find((i) => i.title === PLAYBACK.direct.title)!.id
  const total = PLAYBACK.direct.seconds * 1000
  for (const [name, avatar, at] of [
    ['Мама', 'grad-3', 22_000],
    ['Папа', 'grad-4', 30_000],
    ['Бабушка', 'grad-5', 31_000],
  ] as const) {
    const r = await page.request.post('/api/users', { data: { name, avatar } })
    expect(r.status(), await r.text()).toBe(201)
    const id = ((await r.json()) as { id: number }).id
    const ctx = await playwrightRequest.newContext({ baseURL })
    expect((await ctx.post('/api/login', { data: { userId: id } })).ok()).toBeTruthy()
    const s = await ctx.put(`/api/progress/${movie}`, { data: { positionMs: at, durationMs: total } })
    expect(s.status(), await s.text()).toBe(204)
    await ctx.dispose()
  }
  return movie
}

for (const where of ['desktop', 'phone'] as const) {
  test(`${where}: the family on the timeline, a jump to Мама and back`, async ({ browser, vaultURL }) => {
    const page = await newPage(browser, vaultURL, where)
    const movie = await family(page, vaultURL)

    await page.goto(`/watch/${movie}?t=8`)
    const player = page.getByTestId('player')
    await expect(player).toHaveAttribute('data-mode', 'direct')
    const slider = page.getByRole('slider', { name: 'Позиция' })
    await expect.poll(async () => Number(await slider.getAttribute('aria-valuemax'))).toBeGreaterThan(30)
    // paused at 0:08: the controls stay up, the frame is stable
    await page.evaluate(() => document.querySelector('video')!.pause())
    await page.evaluate(() => (document.querySelector('video')!.currentTime = 8))
    await expect(slider).toHaveAttribute('aria-valuenow', '8')

    const marks = page.getByRole('group', { name: 'Семья' })
    await expect(marks.getByRole('button')).toHaveCount(3)
    const mom = marks.getByRole('button', { name: 'Перейти на место профиля «Мама» — 0:22' })
    await expect(mom).toBeVisible()
    if (where === 'desktop') {
      await mom.hover()
      await expect(mom.getByText('Мама · 0:22')).toBeVisible()
    }
    await shot(page, `markers-${where}`)

    await mom.click()
    await expect(slider).toHaveAttribute('aria-valuenow', '22')
    const back = page.getByRole('button', { name: 'Вернуться к 0:08' })
    await expect(back).toBeVisible()
    await page.mouse.move(5, 5)
    await shot(page, `return-${where}`)

    // only looked at: a save now keeps the admin's 0:08, never Мама's 0:22
    await page.evaluate(() => document.querySelector('video')!.dispatchEvent(new Event('pause')))
    await page.waitForTimeout(500)
    const mine = ((await (await page.request.get(`/api/progress/${movie}`)).json()) as { positionMs?: number }).positionMs ?? 0
    expect(mine).toBeGreaterThanOrEqual(8000)
    expect(mine).toBeLessThan(9000)

    await back.click()
    await expect(slider).toHaveAttribute('aria-valuenow', '8')
    await expect(page.getByRole('button', { name: /Вернуться к/ })).toHaveCount(0)
    await page.context().close()
  })
}
