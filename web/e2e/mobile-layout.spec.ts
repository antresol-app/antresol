// The phone web layout at 390 × 844 and 375 × 667 (the owner's iPhone
// Safari, 2026-11-24):
// - «Статистика»: a section's title keeps the section spacing from the group
//   above it when the two columns stack (it sat flush against the card);
// - the top bar: once the page scrolls, what slides under it is dimmed down
//   to the bar's own bottom edge, so a bright card never shows between the
//   brand and the avatar (it did; iOS Safari draws no blur on a fixed bar, so
//   the dim alone must hide it — Playwright's WebKit behaves the same);
// - the tab bar: the capsule and the search on one line, equal insets from
//   the screen's sides, the lens inside the capsule under the current tab,
//   and a page's last row clear of the bar; a coloured settings icon under
//   «Главная» shows through the glass muted, not as a vivid blob that reads
//   as a stray selection (it did, with a 0.52 tint and saturate 1.8).
import type { Page } from '@playwright/test'
import type { Stats } from '../src/api/types'
import { homeFixture } from './fixtures/home'
import { mockLibrary } from './helpers'
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'empty', hasTouch: true })

const SIZES = [
  { width: 390, height: 844 },
  { width: 375, height: 667 },
]

const stats: Stats = {
  disk: { total: 4e12, used: 2.6e12, free: 1.4e12, categories: [{ id: 'movies', bytes: 1.8e12 }, { id: 'series', bytes: 0.8e12 }] },
  counts: { movies: 412, series: 58, episodes: 2310, albums: 120, tracks: 1500, books: 40, audiobooks: 6, comics: 3 },
  watchedByUser: [
    { user: 'admin', avatar: 'grad-6', hours30d: 2.7 },
    { user: 'Bea', avatar: 'grad-6', hours30d: 2.3 },
    { user: 'Cleo', avatar: 'grad-7', hours30d: 0.02 },
  ],
  topItems30d: Array.from({ length: 8 }, (_, i) => ({ id: 100 + i, kind: 'movie' as const, title: `Фильм номер ${i + 1}`, poster: '', viewers: 2, hours: 1.7 - i * 0.2 })),
}

async function phone(page: Page, size: { width: number; height: number }) {
  await page.setViewportSize(size)
  await setupAdmin(page.request)
  await page.route('**/api/stats', (route) => route.fulfill({ json: stats }))
}

type Clip = { x: number; y: number; width: number; height: number }

/** The brightest pixel (0…255, the max of R, G, B) of a region of the screen, decoded in the page. */
const brightest = (page: Page, clip: Clip) => pixels(page, clip, (r, g, b) => Math.max(r, g, b))
/** How green the greenest pixel of a region is: G over the mean of R and B. */
const greenest = (page: Page, clip: Clip) => pixels(page, clip, (r, g, b) => g - (r + b) / 2)

async function pixels(page: Page, clip: Clip, f: (r: number, g: number, b: number) => number) {
  const png = (await page.screenshot({ clip })).toString('base64')
  const values = await page.evaluate(async (src) => {
    const img = new Image()
    img.src = `data:image/png;base64,${src}`
    await img.decode()
    const c = document.createElement('canvas')
    c.width = img.naturalWidth
    c.height = img.naturalHeight
    const g = c.getContext('2d')!
    g.drawImage(img, 0, 0)
    return [...g.getImageData(0, 0, c.width, c.height).data]
  }, png)
  let max = 0
  for (let i = 0; i < values.length; i += 4) max = Math.max(max, f(values[i]!, values[i + 1]!, values[i + 2]!))
  return max
}

for (const size of SIZES) {
  test.describe(`${size.width} × ${size.height}`, () => {
    test('Статистика: each section title keeps its spacing from the group above', async ({ page }) => {
      await phone(page, size)
      await page.goto('/manage/stats')
      const top = page.getByRole('heading', { level: 3, name: 'Смотрели чаще всего за 30 дней' })
      await expect(top).toBeVisible()
      const gaps = await page.evaluate(() =>
        [...document.querySelectorAll('main h3')].map((h) => {
          // the nearest block drawn above the title (the group of the column before it)
          const t = h.getBoundingClientRect()
          let above = -Infinity
          for (const el of document.querySelectorAll('main ul, main ol, main [class*="card"]')) {
            const r = el.getBoundingClientRect()
            if (r.bottom <= t.top + 0.5 && r.right > t.left && r.left < t.right) above = Math.max(above, r.bottom)
          }
          return { title: h.textContent, gap: above === -Infinity ? null : Math.round(t.top - above) }
        }),
      )
      const stacked = gaps.filter((g) => g.gap !== null)
      expect(stacked.length).toBeGreaterThan(0)
      // the section spacing (--s-8 = 32; the first group title on a page has none of its own)
      for (const g of stacked) expect(g.gap!, `${g.title}`).toBeGreaterThanOrEqual(28)
    })

    test('the top bar dims what scrolls under it down to its bottom edge', async ({ page }) => {
      await phone(page, size)
      await page.goto('/manage/stats')
      await expect(page.getByRole('heading', { level: 3, name: 'Смотрели чаще всего за 30 дней' })).toBeVisible()
      // a white card scrolled under the bar: the worst case of a stat tile
      await page.evaluate(() => {
        const d = document.createElement('div')
        d.id = 'probe'
        d.style.cssText = 'height: 600px; background: #fff'
        document.querySelector('main section')!.prepend(d)
      })
      await page.evaluate(() => window.scrollTo(0, document.getElementById('probe')!.offsetTop + 200))
      const header = page.locator('header[data-top-bar]')
      await expect.poll(() => header.evaluate((h) => getComputedStyle(h, '::after').opacity)).toBe('1')
      const bar = (await header.boundingBox())!
      const brand = (await header.getByRole('link').first().boundingBox())!
      const avatar = (await header.getByRole('button').last().boundingBox())!
      // between the brand and the avatar, the lowest rows of the bar
      const clip = { x: brand.x + brand.width + 8, y: bar.y + bar.height - 10, width: avatar.x - (brand.x + brand.width) - 16, height: 8 }
      expect(clip.width).toBeGreaterThan(40)
      expect(await brightest(page, clip)).toBeLessThan(64)
    })

    test('the tab bar: one line, equal insets, the lens under the current tab, the page clears it', async ({ page }) => {
      await phone(page, size)
      await mockLibrary(page, homeFixture)
      await page.goto('/')
      const bar = page.getByTestId('tabbar')
      await expect(bar).toBeVisible()
      const m = await bar.evaluate((nav) => {
        const box = (el: Element) => el.getBoundingClientRect().toJSON() as DOMRect
        const capsule = nav.querySelector('ul')!.parentElement!
        return {
          vw: document.documentElement.clientWidth,
          vh: window.innerHeight,
          capsule: box(capsule),
          search: box(nav.querySelector(':scope > a')!),
          lens: box(nav.querySelector('[style*="--i"]')!),
          current: box(nav.querySelector('ul a[aria-current="page"]')!),
        }
      })
      // on one line, the same height
      expect(Math.abs(m.capsule.top - m.search.top)).toBeLessThanOrEqual(0.5)
      expect(Math.abs(m.capsule.bottom - m.search.bottom)).toBeLessThanOrEqual(0.5)
      // equal insets from the sides, clear of the bottom edge
      expect(Math.abs(m.capsule.left - (m.vw - m.search.right))).toBeLessThanOrEqual(0.5)
      expect(m.capsule.left).toBeGreaterThanOrEqual(8)
      expect(m.vh - m.capsule.bottom).toBeGreaterThanOrEqual(8)
      // the lens: inside the capsule, centred on the current tab
      expect(m.lens.left).toBeGreaterThanOrEqual(m.capsule.left)
      expect(m.lens.right).toBeLessThanOrEqual(m.capsule.right)
      expect(m.lens.top).toBeGreaterThanOrEqual(m.capsule.top)
      expect(m.lens.bottom).toBeLessThanOrEqual(m.capsule.bottom)
      expect(Math.abs((m.lens.left + m.lens.right) / 2 - (m.current.left + m.current.right) / 2)).toBeLessThanOrEqual(1)

      // a long page's last row ends above the bar once scrolled to the end
      await page.goto('/manage/stats')
      const last = page.getByRole('listitem').filter({ hasText: 'Фильм номер 8' })
      await expect(last).toBeVisible()
      await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight))
      // (scrolled down, the bar folds: the same height, the same line)
      await expect(bar).toHaveAttribute('data-minimized', 'true')
      const row = (await last.boundingBox())!
      const capsule = (await bar.locator('ul').locator('..').boundingBox())!
      expect(row.y + row.height).toBeLessThanOrEqual(capsule.y)
    })

    test('a coloured icon under the tab bar shows through muted', async ({ page, browserName }) => {
      test.skip(browserName !== 'chromium', 'Playwright’s WebKit draws no backdrop filter to measure')
      await phone(page, size)
      await page.goto('/manage/settings')
      const bar = page.getByTestId('tabbar')
      await expect(bar).toBeVisible()
      const capsule = (await bar.locator('ul').locator('..').boundingBox())!
      // a settings icon (the system green) right under «Главная»
      await page.evaluate((c) => {
        const d = document.createElement('div')
        d.style.cssText = `position: fixed; left: ${c.x + 24}px; top: ${c.y + 7}px; width: 44px; height: 44px; border-radius: 10px; background: #30d158`
        document.body.append(d)
      }, capsule)
      await page.waitForTimeout(300)
      // 0.52 / 1.8 measured 72 here; 0.62 / 1.2: about 41
      expect(await greenest(page, { x: capsule.x, y: capsule.y, width: 90, height: capsule.height })).toBeLessThan(55)
    })
  })
}
