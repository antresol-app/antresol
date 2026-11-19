// «О сервере» from a phone to a wide window: its row labels («Видео»,
// «Версия») stay whole words on one line, the value or the reason beside or
// under them wraps instead. A stacked value with a long status sentence once
// squeezed «Видео» to one letter per line in a 445 pt sheet (ui/List gave
// the control the row). The GPU's reason here is the longest the server
// gives (no permission to the render node).
import type { Page } from '@playwright/test'
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'empty' })

const detail = '[AVHWDeviceContext @ 0x1] No VA display found for device /dev/dri/renderD128.'

/** The server's answer for a render node the server's user may not open. */
async function noPermission(page: Page) {
  await page.route('**/api/settings/transcoding', async (route) => {
    const r = await route.fetch()
    const v = await r.json()
    v.devices = ['/dev/dri/renderD128']
    v.status = {
      ...v.status,
      mode: 'none',
      requested: 'auto',
      name: 'software',
      probes: [{ mode: 'vaapi', ok: false, error: detail }],
      problem: { kind: 'permission', mode: 'vaapi', device: '/dev/dri/renderD128', gid: 105, encoder: 'h264_vaapi', detail },
    }
    await route.fulfill({ response: r, json: v })
  })
}

for (const width of [320, 390, 445, 1440]) {
  test(`«О сервере» at ${width} pt: labels are whole words on one line`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 900 })
    await setupAdmin(page.request)
    await noPermission(page)
    await page.goto('/settings')
    await page.getByRole('button', { name: 'О сервере' }).click()
    const sheet = page.getByRole('dialog')
    await expect(sheet.getByText('Процессор: нет доступа к /dev/dri/renderD128 — добавьте в group_add группу 105')).toBeVisible()
    for (const label of ['Видео', 'Версия', 'Лицензия']) {
      const el = sheet.getByText(label, { exact: true })
      const box = await el.evaluate((n) => {
        const r = n.getBoundingClientRect()
        return { h: r.height, line: parseFloat(getComputedStyle(n).lineHeight), over: n.scrollWidth - n.clientWidth }
      })
      expect(box.h, `${label} at ${width}: one line`).toBeLessThan(box.line * 1.5)
      expect(box.over, `${label} at ${width}: not cut`).toBeLessThanOrEqual(0)
    }
    expect(await page.evaluate(() => document.documentElement.scrollWidth), 'no sideways scroll').toBe(width)
    await sheet.screenshot({ path: info.outputPath(`about-${width}.png`) })
  })
}

// «Обработка → Перекодирование» says the same reason and the fix in full: the group_add
// lines with the node's group and ffmpeg's own words.
for (const width of [390, 1440]) {
  test(`«Перекодирование» at ${width} pt: the reason, its fix and ffmpeg's words`, async ({ page }, info) => {
    await page.setViewportSize({ width, height: 1100 })
    await setupAdmin(page.request)
    await noPermission(page)
    await page.goto('/manage/settings/processing')
    await expect(page.getByText(/принадлежит группе 105/)).toBeVisible()
    await expect(page.getByText(detail)).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth), 'no sideways scroll').toBe(width)
    await page.screenshot({ path: info.outputPath(`transcoding-${width}.png`), fullPage: true })
  })
}
