// A settings row's label and its value never touch (the owner's iPhone: «Почта и
// уведомления» was overlapped by its value «Настроена»): «Управление»'s list on a
// phone, at the widths real phones have, in both languages, with values set that
// are short («Настроена»), long (a model name without spaces) and a choice
// («1080p»); and the profile's own list (ui/List's link rows with a value).
import type { APIRequestContext, Page } from '@playwright/test'
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

/** A mailbox set up (the value «Настроена»/«Set up») and a model with a long name (the value is cut). */
async function seedValues(request: APIRequestContext) {
  const current = (await (await request.get('/api/settings')).json()) as Record<string, unknown>
  const saved = await request.put('/api/settings', {
    data: {
      ...current,
      mail: { host: 'smtp.example.com', port: 587, username: 'reader@example.com', from: 'reader@example.com', password: 'secret-1' },
      ai: { url: 'http://127.0.0.1:4000/v1', model: 'a-model-name-without-any-spaces-1234567890', key: '' },
    },
  })
  expect(saved.ok(), await saved.text()).toBeTruthy()
}

/** The two lists at the page's width: nothing scrolls sideways, no row has a problem. */
async function checkLists(page: Page, width: number, lang: string) {
  for (const path of ['/manage', '/settings']) {
    await page.goto(path)
    await page.locator('#main h1').first().waitFor()
    // the summaries come with the settings: wait for the one that is set
    if (path === '/manage') await expect(page.locator('a[class*="_navItem_"] [class*="_navValue_"]').first()).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth), `${path} at ${width} scrolls sideways`).toBeLessThanOrEqual(width)
    expect(await rowProblems(page), `${path} at ${width}, ${lang}`).toEqual([])
  }
}

for (const lang of ['ru', 'en']) {
  test(`phone list, ${lang}: a label never touches its value, at every phone width`, async ({ page }) => {
    await page.addInitScript((l) => localStorage.setItem('vault.uiLanguage', l), lang)
    await setupAdmin(page.request)
    await seedValues(page.request)
    for (const width of WIDTHS) {
      await page.setViewportSize({ width, height: 900 })
      await checkLists(page, width, lang)
    }
  })
}

// An iPhone as the engine sees it: the page of a touch device with its own
// viewport (375×812 and 390×844 pt, a 3x screen), which the webkit project
// draws in Safari's engine — where the owner saw «Set up» over «Mail and
// notifications». ROWS_SHOTS=<dir> also saves a picture of each list there.
const IPHONES = [
  { width: 375, height: 812 },
  { width: 390, height: 844 },
]
for (const lang of ['ru', 'en']) {
  test(`iPhone window, ${lang}: a label never touches its value`, async ({ browser, baseURL, page: admin }, info) => {
    await setupAdmin(admin.request)
    await seedValues(admin.request)
    const storageState = await admin.context().storageState()
    for (const { width, height } of IPHONES) {
      const ctx = await browser.newContext({ baseURL, storageState, viewport: { width, height }, isMobile: true, hasTouch: true, deviceScaleFactor: 3, locale: lang === 'ru' ? 'ru-RU' : 'en-US', serviceWorkers: 'block' })
      try {
        await ctx.addInitScript((l) => localStorage.setItem('vault.uiLanguage', l), lang)
        const page = await ctx.newPage()
        await checkLists(page, width, lang)
        const dir = process.env.ROWS_SHOTS
        if (dir) {
          for (const [path, name] of [['/manage', 'manage'], ['/settings', 'settings']] as const) {
            await page.goto(path)
            await page.locator('#main h1').first().waitFor()
            await page.waitForTimeout(500)
            await page.screenshot({ path: `${dir}/${info.project.name}-${name}-${lang}-${width}x${height}.png`, fullPage: true })
          }
        }
      } finally {
        await ctx.close()
      }
    }
  })
}
