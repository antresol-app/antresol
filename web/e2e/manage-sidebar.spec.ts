// «Настройки»' sidebar sticks beside the section and, when the window is
// shorter than the list, scrolls inside itself: its last row («Аккаунты и
// синхронизация») is reachable and opens at a desktop height and on a short
// window alike.
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'empty' })

const SIZES: [number, number][] = [
  [1440, 900],
  [1280, 560],
]
for (const [width, height] of SIZES) {
  test(`sidebar at ${width}×${height}: scrolls to its last row`, async ({ page }) => {
    await page.setViewportSize({ width, height })
    await setupAdmin(page.request)
    await page.goto('/manage/health')
    const side = page.locator('aside')
    const last = side.getByRole('link', { name: 'Аккаунты и синхронизация' })
    // the list may be longer than the window: it scrolls inside, never past it
    const box = (await side.boundingBox())!
    expect(box.y + box.height).toBeLessThanOrEqual(height)
    await side.hover()
    await page.mouse.wheel(0, 2000)
    await expect(last).toBeInViewport()
    const row = (await last.boundingBox())!
    expect(row.y + row.height).toBeLessThanOrEqual(box.y + box.height + 0.5)
    await last.click()
    await expect(page).toHaveURL(/\/manage\/settings\/accounts$/)
  })
}
