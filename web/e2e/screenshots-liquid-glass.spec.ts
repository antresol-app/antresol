// Liquid Glass on the phone's floating bars (design/glass.css .glass-liquid,
// ui/liquidGlass.ts), on a 3x phone: Chromium bends the backdrop at the rims
// (an SVG displacement map of each element's size, --liquid-lens), every
// engine draws the rim, the glow and the sheen, and the selected tab's lens
// stretches on its way like a drop. LIQUID_SHOTS=<dir> also saves screenshots
// of the bar (project visual: new-headless Chrome draws glass as a browser
// does).
import path from 'node:path'
import type { Page } from '@playwright/test'
import type { Card, Home } from '../src/api/types'
import { homeFixture } from './fixtures/home'
import { mockLibrary } from './helpers'
import { MUSIC } from './music-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

const SHOTS = process.env.LIQUID_SHOTS
const engine = () => (test.info().project.name === 'webkit' ? 'webkit' : 'chromium')
/** The bottom band of the screen, where the bars float. */
const shot = async (page: Page, name: string) => {
  if (!SHOTS) return
  const { width, height } = page.viewportSize()!
  await page.screenshot({
    path: path.join(SHOTS, `${name}-${engine()}.png`),
    clip: { x: 0, y: height - 130, width, height: 130 },
  })
}

const full: Home = {
  ...homeFixture,
  shelves: homeFixture.shelves.map((s, n) => ({
    ...s,
    cards: Array.from({ length: 20 }, (_, i): Card => ({
      ...s.cards[i % s.cards.length]!,
      id: 10_000 * (n + 1) + i,
    })),
  })),
}

async function phone(page: Page, width: number) {
  await page.setViewportSize({ width, height: 844 })
  await setupAdmin(page.request)
}

/** What each glass part draws with: its refraction filter and its rim. */
const material = (page: Page) =>
  page.getByTestId('tabbar').evaluate((nav) => {
    const capsule = nav.querySelector('ul')!.parentElement!
    const search = nav.querySelector(':scope > a')!
    const of = (el: Element) => ({
      lens: (el as HTMLElement).style.getPropertyValue('--liquid-lens'),
      filter: getComputedStyle(el).backdropFilter || getComputedStyle(el).getPropertyValue('-webkit-backdrop-filter'),
      rim: getComputedStyle(el, '::before').content !== 'none',
    })
    return {
      capsule: of(capsule),
      search: of(search),
      filters: document.querySelectorAll('svg filter[id^="liquid-"]').length,
    }
  })

test.use({ deviceScaleFactor: 3 })

test.describe('Liquid Glass', () => {
  test.use({ library: 'empty' })

  for (const width of [320, 390, 430]) {
    test(`the bars are lenses, ${width} pt`, async ({ page }) => {
      await phone(page, width)
      await mockLibrary(page, full)
      await page.goto('/')
      await expect(page.getByRole('region', { name: 'Рекомендуем' })).toBeVisible()
      // the map is drawn once the size holds
      await page.waitForTimeout(700)
      const m = await material(page)
      expect(m.capsule.rim && m.search.rim).toBe(true)
      if (engine() === 'chromium') {
        expect(m.capsule.lens).toMatch(/^url\(#liquid-/)
        expect(m.search.lens).toMatch(/^url\(#liquid-58x58-29-/)
        expect(m.capsule.filter).toContain('url(')
        expect(m.filters).toBeGreaterThanOrEqual(2)
      } else {
        // WebKit accepts url() in backdrop-filter and then draws nothing at all: never asked to
        expect([m.capsule.lens, m.search.lens, m.filters]).toEqual(['', '', 0])
      }
      await shot(page, `liquid-${width}`)
    })
  }

  test('the rim bends what passes under it (a test pattern behind the bar)', async ({ page }) => {
    await phone(page, 390)
    await mockLibrary(page, full)
    await page.goto('/')
    await page.evaluate(() => {
      const d = document.createElement('div')
      d.style.cssText =
        'position:fixed;inset:auto 0 0 0;height:160px;z-index:40;background:repeating-linear-gradient(90deg,#e33 0 6px,#fff 6px 12px,#36f 12px 18px,#111 18px 24px);font:700 22px/40px system-ui;color:#000'
      d.textContent = 'Liquid Glass Liquid Glass Liquid Glass'
      document.body.append(d)
    })
    await page.waitForTimeout(700)
    await shot(page, 'liquid-pattern-390')
  })

  test('the lens stretches on its way to another tab and settles', async ({ page }) => {
    await phone(page, 390)
    await mockLibrary(page, full)
    await page.goto('/')
    await page.waitForTimeout(700)
    const bar = page.getByTestId('tabbar')
    // the widest the lens gets on its way, frame by frame, from the press on
    const widest = bar.evaluate(
      (nav) =>
        new Promise<number>((done) => {
          let max = 1
          const t0 = performance.now()
          const tick = () => {
            const el = nav.querySelector('[style*="--i"]')
            if (el) max = Math.max(max, new DOMMatrix(getComputedStyle(el).transform).a)
            if (performance.now() - t0 < 1500) requestAnimationFrame(tick)
            else done(max)
          }
          tick()
        }),
    )
    await bar.getByRole('link', { name: 'ТВ' }).click()
    await page.waitForTimeout(110)
    await shot(page, 'liquid-390-droplet')
    expect(await widest).toBeGreaterThan(1.05)
    expect(await bar.locator('[style*="--i"]').evaluate((el) => getComputedStyle(el).transform)).toBe('none')
  })

  test('Reduce motion: the lens moves without stretching', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await phone(page, 390)
    await mockLibrary(page, full)
    await page.goto('/')
    await page.waitForTimeout(300)
    await page.getByTestId('tabbar').getByRole('link', { name: 'Медиа' }).click()
    await page.waitForTimeout(60)
    expect(
      await page
        .getByTestId('tabbar')
        .locator('[style*="--i"]')
        .evaluate((el) => getComputedStyle(el).transform),
    ).toBe('none')
  })

  // Playwright cannot emulate the media feature itself: script is told it is on (the CSS side is glass.css's own rule)
  test('Reduce transparency: nothing bent, the rim stays', async ({ page }) => {
    await page.addInitScript(() => {
      const mm = window.matchMedia.bind(window)
      window.matchMedia = (q: string) =>
        q.includes('reduced-transparency')
          ? ({
              ...mm(q),
              matches: true,
              media: q,
              addEventListener() {},
              removeEventListener() {},
            } as MediaQueryList)
          : mm(q)
    })
    await phone(page, 390)
    await mockLibrary(page, full)
    await page.goto('/')
    await page.waitForTimeout(700)
    const m = await material(page)
    expect([m.capsule.lens, m.search.lens]).toEqual(['', ''])
    expect(m.capsule.rim).toBe(true)
  })
})

test.describe('with the player', () => {
  test.use({ library: 'music' })

  test('the folded bar with the player: three lenses in a row', async ({ page }) => {
    await phone(page, 390)
    await scanLibrary(page.request, (s) => s.albums >= 2)
    await page.goto('/music')
    await page
      .getByRole('link', { name: new RegExp(MUSIC.album) })
      .first()
      .click()
    await page.getByRole('button', { name: 'Слушать' }).click()
    const mini = page.getByRole('complementary', { name: 'Сейчас играет' })
    await expect(mini).toContainText(MUSIC.tracks[0]!)
    await page.evaluate(() => {
      const d = document.createElement('div')
      d.style.height = '3000px'
      document.querySelector('main')!.append(d)
    })
    await page.mouse.wheel(0, 1200)
    await expect(page.getByTestId('tabbar')).toHaveAttribute('data-minimized')
    await page.waitForTimeout(900)
    const lens = await mini.evaluate((el) => (el as HTMLElement).style.getPropertyValue('--liquid-lens'))
    if (engine() === 'chromium') expect(lens).toMatch(/^url\(#liquid-/)
    else expect(lens).toBe('')
    await shot(page, 'liquid-390-player-folded')
  })
})
