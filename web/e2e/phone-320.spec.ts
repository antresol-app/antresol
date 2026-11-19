// The narrowest phone (320 pt, an iPhone SE), in Russian (the longest words)
// and in English: nothing scrolls sideways, a settings row keeps its chevron
// and cuts a long value with an ellipsis, and a plain row's text keeps a
// column it can be read in (its control goes under it) instead of breaking
// inside words. The causes were in the primitives (ui/Button, ui/List, the
// section list's rows), so this walks the screens that use them.
import type { Page } from '@playwright/test'
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'empty', fakes: true, viewport: { width: 320, height: 700 } })

const PAGES = ['/', '/movies', '/series', '/manage', '/settings', '/settings/accounts', '/manage/settings/library', '/manage/settings/processing', '/manage/settings/kindle', '/manage/health', '/manage/tasks', '/manage/channels']

const widths = () => ({ inner: window.innerWidth, scroll: document.documentElement.scrollWidth })

/** What a row of the section list or a plain settings row does wrong, by CSS-module class prefix. */
async function rowProblems(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const bad: string[] = []
    // the section list: a row is a link whose content must fit it, the chevron inside
    for (const a of document.querySelectorAll<HTMLElement>('nav a[class*="_navItem_"]')) {
      if (a.scrollWidth > a.clientWidth) bad.push(`row "${a.textContent}" overflows by ${a.scrollWidth - a.clientWidth}px`)
      const chevron = a.querySelector('svg:last-child')
      if (chevron && chevron.getBoundingClientRect().right > a.getBoundingClientRect().right) bad.push(`row "${a.textContent}": chevron out of the row`)
    }
    // a plain row: the text keeps a readable column (--row-text-min, 9rem); shrink rows cut their control instead
    for (const t of document.querySelectorAll<HTMLElement>('[class*="_row_"] > [class*="_text_"]')) {
      const row = t.parentElement!
      if (/_(shrink|wide)_/.test(row.className) || getComputedStyle(row).display === 'none') continue
      if (t.getBoundingClientRect().width < 140) bad.push(`text "${t.textContent?.slice(0, 40)}" is ${Math.round(t.getBoundingClientRect().width)}px wide`)
    }
    return bad
  })
}

for (const lang of ['ru', 'en']) {
  test(`320 pt, ${lang}: no sideways scroll, rows keep their chevron and their text column`, async ({ page }) => {
    await page.addInitScript((l) => localStorage.setItem('vault.uiLanguage', l), lang)
    await setupAdmin(page.request)
    for (const path of PAGES) {
      await page.goto(path)
      await page.waitForLoadState('networkidle')
      expect(await page.evaluate(widths), `${path} scrolls sideways`).toEqual({ inner: 320, scroll: 320 })
      expect(await rowProblems(page), path).toEqual([])
    }
  })
}
