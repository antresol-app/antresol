// A settings row's label and its value never touch (the owner's iPhone: «Почта и
// уведомления» was overlapped by its value «Настроена»): «Управление»'s list on a
// phone, at the widths real phones have, in both languages, with values set that
// are short («Настроена»), long (a model name without spaces) and a choice
// («1080p»); and the profile's own list (ui/List's link rows with a value).
import type { Page } from '@playwright/test'
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'empty', fakes: true })

const WIDTHS = [280, 320, 360, 375, 390, 414, 430]

/** What a row of a list does wrong: its label's text reaches its value or its chevron, the value reaches the chevron, something leaves the row. */
async function rowProblems(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const bad: string[] = []
    type R = { left: number; right: number; top: number; bottom: number }
    const touch = (a: R, b: R) => Math.min(a.right, b.right) - Math.max(a.left, b.left) > 0.5 && Math.min(a.bottom, b.bottom) - Math.max(a.top, b.top) > 0.5
    // the lines of a label's text, not its box (a box may be wider than its words)
    const lines = (el: Element): R[] => {
      const r = document.createRange()
      r.selectNodeContents(el)
      return [...r.getClientRects()].filter((x) => x.width > 0.5)
    }
    // «Управление»'s section list (Manage.module.css: navItem) and ui/List's link rows (linkButton)
    const rows = [
      ...[...document.querySelectorAll<HTMLElement>('a[class*="_navItem_"]')].map((row) => ({ row, label: row.querySelector('[class*="_navLabel_"]'), value: row.querySelector('[class*="_navValue_"]') })),
      ...[...document.querySelectorAll<HTMLElement>('[class*="_linkButton_"]')].map((row) => ({ row, label: row.querySelector('[class*="_text_"]'), value: row.querySelector('[class*="_value_"]') })),
    ]
    for (const { row, label, value } of rows) {
      if (!label) continue
      const name = label.textContent?.trim().slice(0, 40)
      const box = row.getBoundingClientRect()
      // the chevron is the row's own last icon (the tile's glyph is inside its tile)
      const chevron = row.querySelector(':scope > svg:last-child')?.getBoundingClientRect()
      const v = value?.getBoundingClientRect()
      const hasValue = Boolean(v && v.width > 0 && value?.textContent?.trim())
      const text = lines(label)
      if (hasValue && text.some((l) => touch(l, v!))) bad.push(`"${name}": its text reaches the value "${value!.textContent}"`)
      if (hasValue && chevron && touch(v!, chevron)) bad.push(`"${name}": the value reaches the chevron`)
      if (chevron && text.some((l) => touch(l, chevron))) bad.push(`"${name}": its text reaches the chevron`)
      if (chevron && chevron.right > box.right + 0.5) bad.push(`"${name}": the chevron is out of the row`)
      if (hasValue && v!.right > box.right + 0.5) bad.push(`"${name}": the value is out of the row`)
      if (text.some((l) => l.right > box.right + 0.5)) bad.push(`"${name}": its text is out of the row`)
    }
    return bad
  })
}

for (const lang of ['ru', 'en']) {
  test(`phone list, ${lang}: a label never touches its value, at every phone width`, async ({ page }) => {
    await page.addInitScript((l) => localStorage.setItem('vault.uiLanguage', l), lang)
    await setupAdmin(page.request)
    // a mailbox set up (the value «Настроена»/«Set up») and a model with a long name (the value is cut)
    const current = (await (await page.request.get('/api/settings')).json()) as Record<string, unknown>
    const saved = await page.request.put('/api/settings', {
      data: {
        ...current,
        mail: { host: 'smtp.example.com', port: 587, username: 'reader@example.com', from: 'reader@example.com', password: 'secret-1' },
        ai: { url: 'http://127.0.0.1:4000/v1', model: 'a-model-name-without-any-spaces-1234567890', key: '' },
      },
    })
    expect(saved.ok(), await saved.text()).toBeTruthy()
    for (const width of WIDTHS) {
      await page.setViewportSize({ width, height: 900 })
      for (const path of ['/manage', '/settings']) {
        await page.goto(path)
        await page.locator('#main h1').first().waitFor()
        // the summaries come with the settings: wait for the one that is set
        if (path === '/manage') await expect(page.locator('a[class*="_navItem_"] [class*="_navValue_"]').first()).toBeVisible()
        expect(await page.evaluate(() => document.documentElement.scrollWidth), `${path} at ${width} scrolls sideways`).toBeLessThanOrEqual(width)
        expect(await rowProblems(page), `${path} at ${width}, ${lang}`).toEqual([])
      }
    }
  })
}
