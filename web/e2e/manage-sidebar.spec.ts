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

// On a laptop's window the list is longer than the panel: the section that is
// open is scrolled into view (it may be the last), an edge with more rows past
// it fades (so a cut row does not read as the list's end), and the top of the
// list has no fade.
test('sidebar on a laptop window: the open section is in view, the fades say where there is more', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 610 })
  await setupAdmin(page.request)
  const list = page.locator('aside [class*="_sidebarScroll_"]')

  await page.goto('/manage/profiles')
  await expect(list).toHaveAttribute('data-fade-end', 'true')
  await expect(list).not.toHaveAttribute('data-fade-start', /.*/)

  await page.goto('/manage/settings/external')
  const here = list.getByRole('link', { name: 'Внешние сервисы' })
  await expect(list).toHaveAttribute('data-fade-start', 'true')
  await expect(list).not.toHaveAttribute('data-fade-end', /.*/)
  const box = (await list.boundingBox())!
  const row = (await here.boundingBox())!
  expect(row.y).toBeGreaterThanOrEqual(box.y)
  expect(row.y + row.height).toBeLessThanOrEqual(box.y + box.height + 0.5)
  // the page itself never scrolled to show it
  expect(await page.evaluate(() => window.scrollY)).toBe(0)
})
