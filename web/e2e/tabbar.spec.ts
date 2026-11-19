// The phone's tab bar (iOS 26, ui/TabBar) in a real layout: five tabs and the
// square search fit 320…430 pt in both languages at the approved sizes, the
// lens sits under the current tab, «Медиа» holds Музыка and Книги, the bar
// folds on a scroll down (the mini player takes the room it frees) and opens
// on a scroll up, and Reduce motion leaves only the crossfade.
// TABBAR_SHOTS=<dir> also saves a screenshot of each state there (project visual).
import path from 'node:path'
import type { Page } from '@playwright/test'
import type { Card, Home } from '../src/api/types'
import { homeFixture } from './fixtures/home'
import { mockLibrary } from './helpers'
import { MUSIC } from './music-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

const SHOTS = process.env.TABBAR_SHOTS
// only on the project that draws glass as a browser does (playwright.config.ts)
const shot = async (page: Page, name: string) => {
  if (SHOTS && test.info().project.name === 'visual') await page.screenshot({ path: path.join(SHOTS, `${name}.png`) })
}

/** Full shelves: a page long enough to scroll. */
const full: Home = {
  ...homeFixture,
  shelves: homeFixture.shelves.map((s, n) => ({
    ...s,
    cards: Array.from({ length: 20 }, (_, i): Card => ({ ...s.cards[i % s.cards.length]!, id: 10_000 * (n + 1) + i })),
  })),
}

async function phone(page: Page, width: number, lang: 'ru' | 'en' = 'ru') {
  await page.setViewportSize({ width, height: 844 })
  await page.addInitScript((l) => localStorage.setItem('vault.uiLanguage', l), lang)
  await setupAdmin(page.request)
}

/** The bar's parts, measured: the capsule, the lens, the tabs and the search. */
const measure = (page: Page) =>
  page.getByTestId('tabbar').evaluate((nav) => {
    const box = (el: Element | null) => {
      const r = el!.getBoundingClientRect()
      return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, width: r.width, height: r.height }
    }
    const capsule = nav.querySelector('ul')!.parentElement!
    const search = nav.querySelector(':scope > a')!
    const lens = nav.querySelector('[style*="--i"]')
    const tabs = [...nav.querySelectorAll('ul li a')]
    return {
      screen: { width: window.innerWidth, height: window.innerHeight },
      capsule: { ...box(capsule), radius: getComputedStyle(capsule).borderTopLeftRadius },
      search: { ...box(search), radius: getComputedStyle(search).borderTopLeftRadius },
      lens: lens && box(lens),
      current: { ...box(nav.querySelector('ul a[aria-current="page"]')), text: nav.querySelector('ul a[aria-current="page"]')!.textContent },
      tabs: tabs.map((a) => ({ ...box(a), text: a.textContent })),
      labels: [...nav.querySelectorAll('ul li a span')].map((s) => box(s)),
      cut: [...nav.querySelectorAll('ul li a span')].filter((s) => s.scrollWidth > s.clientWidth).map((s) => s.textContent),
      selected: getComputedStyle(nav.querySelector('ul a[aria-current="page"]')!).color,
      other: getComputedStyle(nav.querySelector('ul a:not([aria-current])')!).color,
      minimized: nav.hasAttribute('data-minimized'),
    }
  })

const LABELS = { ru: ['Главная', 'Фильмы', 'Сериалы', 'ТВ', 'Медиа'], en: ['Home', 'Movies', 'Shows', 'TV', 'Media'] }

test.describe('on the generated home page', () => {
  test.use({ library: 'empty' })

  for (const lang of ['ru', 'en'] as const) {
    for (const width of [320, 360, 390, 430]) {
      test(`five tabs and the search at the approved sizes, ${width} pt, ${lang}`, async ({ page }) => {
        await phone(page, width, lang)
        await mockLibrary(page, full)
        await page.goto('/movies')
        await expect(page.getByTestId('tabbar')).toBeVisible()
        // the lens's spring has settled
        await page.waitForTimeout(700)
        const m = await measure(page)
        expect(m.tabs.map((t) => t.text)).toEqual(LABELS[lang])
        expect(m.cut).toEqual([])
        for (let i = 1; i < m.labels.length; i++) expect(m.labels[i]!.left - m.labels[i - 1]!.right).toBeGreaterThanOrEqual(1.5)
        // 58 high, 16 from the sides (8 at 320, gap 8), 26 over the bottom edge, a capsule (radius 29)
        const side = width < 360 ? 8 : 16
        const gap = width < 360 ? 8 : 10
        expect(m.capsule.height).toBe(58)
        expect(m.capsule.left).toBe(side)
        expect(m.screen.height - m.capsule.bottom).toBe(26)
        expect(m.capsule.radius).toBe('29px')
        // the search: a 58 circle, 10 beside the capsule, 16 from the edge
        expect([m.search.width, m.search.height, m.search.radius]).toEqual([58, 58, '29px'])
        expect(m.search.left - m.capsule.right).toBe(gap)
        expect(m.screen.width - m.search.right).toBe(side)
        // equal tabs inside the 4 pt padding; the lens is the current one's
        const w = (m.capsule.width - 8) / 5
        for (const t of m.tabs) expect(t.width).toBeCloseTo(w, 1)
        expect(m.tabs[0]!.left).toBeCloseTo(m.capsule.left + 4, 1)
        expect(m.tabs.map((t) => t.width).every((w) => w >= 44 || width < 360)).toBe(true)
        expect(m.lens).toMatchObject({ height: 50 })
        expect(m.lens!.left).toBeCloseTo(m.current.left, 0)
        expect(m.lens!.width).toBeCloseTo(m.current.width, 0)
        expect(m.current.text).toBe(LABELS[lang][1])
        // the selected tab in the violet, the others white
        expect(m.selected).toBe('rgb(167, 139, 250)')
        expect(m.other).toBe('rgb(245, 245, 247)')
        await shot(page, `tabbar-${width}-${lang}`)
      })
    }
  }

  test('the lens slides to the tab picked; the bar folds on a scroll down and opens on a scroll up', async ({ page }) => {
    await phone(page, 390)
    await mockLibrary(page, full)
    await page.goto('/')
    const bar = page.getByTestId('tabbar')
    await bar.getByRole('link', { name: 'Сериалы' }).click()
    await expect(page).toHaveURL(/\/series$/)
    await page.goto('/')
    await expect(page.getByRole('region', { name: 'Рекомендуем' })).toBeVisible()
    await page.waitForTimeout(700)
    await shot(page, 'tabbar-390-home')

    await page.mouse.wheel(0, 900)
    await expect(bar).toHaveAttribute('data-minimized')
    await page.waitForTimeout(700)
    let m = await measure(page)
    // only the width folds: everything stays 58 high and round
    expect([m.capsule.width, m.capsule.height, m.capsule.radius, m.search.width, m.search.height]).toEqual([58, 58, '29px', 58, 58])
    expect(m.screen.height - m.capsule.bottom).toBe(26)
    // the square shows the current tab's glyph and opens the bar
    const unfold = page.getByRole('button', { name: 'Показать вкладки' })
    await expect(unfold.locator('svg')).toHaveAttribute('data-icon', 'homeFill')
    // the tabs are out of reach (inert) while folded
    await expect(bar.locator('ul')).toHaveAttribute('inert')
    await shot(page, 'tabbar-390-folded')

    await page.mouse.wheel(0, -300)
    await expect(bar).not.toHaveAttribute('data-minimized')
    await page.waitForTimeout(700)
    m = await measure(page)
    expect([m.capsule.height, m.search.width]).toEqual([58, 58])

    await page.mouse.wheel(0, 900)
    await expect(bar).toHaveAttribute('data-minimized')
    await unfold.click()
    await expect(bar).not.toHaveAttribute('data-minimized')
    await expect(bar.getByRole('link', { name: 'Главная' })).toBeFocused()
  })

  test('Reduce motion: the lens and the fold change in place, only the crossfade is left', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await phone(page, 390)
    await mockLibrary(page, full)
    await page.goto('/')
    const bar = page.getByTestId('tabbar')
    await expect(bar).toBeVisible()
    const motion = () =>
      bar.evaluate((nav) => {
        const capsule = getComputedStyle(nav.querySelector('ul')!.parentElement!)
        const lens = getComputedStyle(nav.querySelector('[style*="--i"]')!)
        const tabs = getComputedStyle(nav.querySelector('ul')!)
        return { capsule: capsule.transitionDuration, lens: lens.transitionDuration, tabs: tabs.transitionDuration }
      })
    const d = await motion()
    // width, height, radius: no spring; the lens's left: none, its fade stays
    expect(d.capsule.split(', ').every((v) => v === '0s')).toBe(true)
    expect(d.lens).toBe('0s, 0.25s')
    expect(d.tabs).toBe('0.25s')
    // and the press does not squeeze
    const tab = bar.getByRole('link', { name: 'Фильмы' })
    const b = (await tab.boundingBox())!
    await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2)
    await page.mouse.down()
    expect(await tab.evaluate((a) => getComputedStyle(a).transform)).toBe('none')
    // let go off the tab: no click, no navigation
    await page.mouse.move(b.x + b.width / 2, 200)
    await page.mouse.up()
    await expect(page).toHaveURL(/\/$/)
    await page.mouse.wheel(0, 900)
    await expect(bar).toHaveAttribute('data-minimized')
    // in place at once: no settling wait needed
    const m = await measure(page)
    expect([m.capsule.width, m.capsule.height]).toEqual([58, 58])
  })

  test('keyboard: the tabs and the search take focus with a visible ring; the current tab is aria-current', async ({ page, browserName }) => {
    test.skip(browserName === 'webkit', 'Safari moves Tab past links unless «Press Tab to highlight each item» is on')
    await phone(page, 390)
    await mockLibrary(page, full)
    await page.goto('/tv')
    const bar = page.getByTestId('tabbar')
    await expect(bar.getByRole('link', { name: 'ТВ' })).toHaveAttribute('aria-current', 'page')
    await bar.getByRole('link', { name: 'Сериалы' }).focus()
    await page.keyboard.press('Tab')
    await expect(bar.getByRole('link', { name: 'ТВ' })).toBeFocused()
    expect(await bar.getByRole('link', { name: 'ТВ' }).evaluate((a) => getComputedStyle(a).outlineStyle)).toBe('solid')
    await shot(page, 'tabbar-390-focus')
  })
})

test.describe('«Медиа»', () => {
  test.use({ library: 'music' })

  test('holds Музыка and Книги, keeps /music and /books, and leads back to the one open last', async ({ page }) => {
    await phone(page, 390)
    await page.goto('/books')
    const bar = page.getByTestId('tabbar')
    const media = bar.getByRole('link', { name: 'Медиа' })
    await expect(media).toHaveAttribute('aria-current', 'page')
    const sw = page.getByRole('radiogroup', { name: 'Медиа' })
    await expect(sw.getByRole('radio', { name: 'Книги' })).toBeChecked()
    await shot(page, 'tabbar-390-media-books')
    await sw.getByRole('radio', { name: 'Музыка' }).click()
    await expect(page).toHaveURL(/\/music$/)
    await expect(page.getByRole('heading', { level: 1, name: 'Музыка' })).toBeVisible()
    await expect(media).toHaveAttribute('aria-current', 'page')
    await bar.getByRole('link', { name: 'Главная' }).click()
    await expect(media).not.toHaveAttribute('aria-current')
    await expect(media).toHaveAttribute('href', '/music')
    await media.click()
    await expect(page).toHaveURL(/\/music$/)
  })

  test('folded, the mini player takes the room between the square and the search', async ({ page }) => {
    await phone(page, 390)
    await scanLibrary(page.request, (s) => s.albums >= 2)
    await page.goto('/music')
    await page.getByRole('link', { name: new RegExp(MUSIC.album) }).first().click()
    await page.getByRole('button', { name: 'Слушать' }).click()
    const mini = page.getByRole('complementary', { name: 'Сейчас играет' })
    await expect(mini).toContainText(MUSIC.tracks[0]!)
    await page.waitForTimeout(700)
    await shot(page, 'tabbar-390-player')
    // a page long enough to scroll
    await page.evaluate(() => {
      const d = document.createElement('div')
      d.style.height = '3000px'
      document.querySelector('main')!.append(d)
    })
    const bar = page.getByTestId('tabbar')
    await page.mouse.wheel(0, 1200)
    await expect(bar).toHaveAttribute('data-minimized')
    await page.waitForTimeout(700)
    const m = await measure(page)
    const p = (await mini.boundingBox())!
    expect(p.height).toBe(58)
    expect(await mini.evaluate((el) => getComputedStyle(el).borderTopLeftRadius)).toBe('29px')
    expect(p.y + p.height).toBe(m.capsule.bottom)
    expect(p.x - m.capsule.right).toBe(10)
    expect(m.search.left - (p.x + p.width)).toBe(10)
    // play/pause stays, the track buttons give way
    await expect(mini.getByRole('button', { name: 'Пауза' })).toBeVisible()
    await expect(mini.getByRole('button', { name: 'Следующий трек' })).toBeHidden()
    await shot(page, 'tabbar-390-player-folded')
  })
})
