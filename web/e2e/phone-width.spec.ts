// A phone keeps its width on Главная. Mobile Chromium grows the layout
// viewport to fit whatever overflows the root (innerWidth up to 4x the
// screen) and lays the fixed bars out across it. Card badges like «Звук RU
// EN» carry a visually-hidden word (position: absolute); in a rail its
// containing block was the rail's wrapper, outside the scroller, so the
// words of cards far down the row overflowed the page instead of the rail.
import { devices, type Browser } from '@playwright/test'
import type { Card, Home } from '../src/api/types'
import { homeFixture } from './fixtures/home'
import { mockLibrary } from './helpers'
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'empty' })

const PHONE_W = 390

/** The fixture with full shelves (the server sends up to 20) whose video cards carry the server's badges. */
const full: Home = {
  ...homeFixture,
  shelves: homeFixture.shelves.map((s, n) => ({
    ...s,
    cards: Array.from({ length: 20 }, (_, i): Card => ({ ...s.cards[i % s.cards.length]!, id: 10_000 * (n + 1) + i, badges: ['4K', 'Звук RU EN'] })),
  })),
}

async function phonePage(browser: Browser, baseURL: string) {
  // the device's own context options (iPhone 13: 390 pt, isMobile) without its browser
  const { defaultBrowserType: _, ...phone } = devices['iPhone 13']
  const ctx = await browser.newContext({ ...phone, baseURL, locale: 'ru-RU', colorScheme: 'dark' })
  const page = await ctx.newPage()
  await setupAdmin(page.request)
  await mockLibrary(page, full)
  return page
}

const widths = () => ({ inner: window.innerWidth, scroll: document.documentElement.scrollWidth })

test('Главная on a phone: badges in the rails do not widen the page', async ({ browser, browserName, vaultURL }) => {
  test.skip(browserName !== 'chromium', 'only mobile Chromium grows the layout viewport to fit overflow')
  const page = await phonePage(browser, vaultURL)
  await page.goto('/')
  await expect(page.getByRole('region', { name: 'Рекомендуем' })).toBeVisible()
  await expect(page.getByText('Звук').first()).toBeAttached()
  await page.waitForLoadState('networkidle')
  expect(await page.evaluate(widths)).toEqual({ inner: PHONE_W, scroll: PHONE_W })
  await page.context().close()
})

test('the phone bars stay the screen wide even when something widens the page', async ({ browser, browserName, vaultURL }) => {
  test.skip(browserName !== 'chromium', 'only mobile Chromium grows the layout viewport to fit overflow')
  const page = await phonePage(browser, vaultURL)
  await page.goto('/')
  await expect(page.getByTestId('tabbar')).toBeVisible()
  // any overflow at all (a future bug): the layout viewport grows past the screen
  await page.evaluate(() => {
    const d = document.createElement('div')
    d.style.cssText = 'width: 2000px; height: 1px'
    document.body.append(d)
  })
  await expect.poll(() => page.evaluate(() => window.innerWidth)).toBeGreaterThan(PHONE_W)
  for (const bar of [page.getByTestId('tabbar'), page.locator('header[data-top-bar]')]) {
    const box = (await bar.boundingBox())!
    expect(box.x).toBe(0)
    expect(box.width).toBeLessThanOrEqual(PHONE_W)
  }
  await page.context().close()
})

// Six tabs and the round search fit in 320…430 pt in both languages: no label
// cut, the last tab inside its capsule, the search beside it and on the screen.
for (const lang of ['ru', 'en'] as const) {
  for (const width of [320, 360, 375, 390, 430]) {
    test(`the tab bar fits ${width} pt in ${lang === 'ru' ? 'Russian' : 'English'}`, async ({ browser, vaultURL }) => {
      const { defaultBrowserType: _, ...phone } = devices['iPhone 13']
      const ctx = await browser.newContext({ ...phone, viewport: { width, height: 800 }, baseURL: vaultURL, locale: lang === 'ru' ? 'ru-RU' : 'en-US', colorScheme: 'dark' })
      await ctx.addInitScript((l) => localStorage.setItem('vault.uiLanguage', l), lang)
      const page = await ctx.newPage()
      await setupAdmin(page.request)
      await mockLibrary(page, full)
      await page.goto('/')
      const bar = page.getByTestId('tabbar')
      await expect(bar).toBeVisible()
      const m = await bar.evaluate((nav) => {
        const tabs = nav.querySelector('ul')!.getBoundingClientRect()
        const lis = [...nav.querySelectorAll('ul li')].map((li) => li.getBoundingClientRect())
        const search = nav.querySelector(':scope > a')!.getBoundingClientRect()
        const labels = [...nav.querySelectorAll('ul li a span')].map((s) => ({ cut: s.scrollWidth > s.clientWidth, text: s.textContent }))
        return { tabsLeft: tabs.left, tabsRight: tabs.right, first: lis[0]!.left, last: lis[lis.length - 1]!.right, search: { left: search.left, right: search.right }, labels, screen: window.innerWidth }
      })
      expect(m.labels.filter((l) => l.cut)).toEqual([])
      expect(m.first).toBeGreaterThanOrEqual(m.tabsLeft)
      expect(m.last).toBeLessThanOrEqual(m.tabsRight)
      expect(m.search.left).toBeGreaterThanOrEqual(m.tabsRight)
      expect(m.search.right).toBeLessThanOrEqual(m.screen)
      await ctx.close()
    })
  }
}
