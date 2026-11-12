// «ТВ» round 5 at 375 pt (a phone) and 1440 (a desktop window): the channel
// editor's «Состав канала» (search, folders, the lineup dragged by its
// handle) and «Логотип», channel logos in the list, and the live player's
// «Звук и субтитры» with 8 voices and 6 subtitle languages — everything
// inside the window, a long menu scrolled inside itself. The API is mocked
// (page.route) on top of a real bin/vault for the session.
import type { Page } from '@playwright/test'
import { open } from './tv-r5-media'
import { expect, test } from './vault'

test.use({ library: 'empty' })

/** Elements past the window's sides that no narrower scroller or clip holds. */
const outside = () => {
  const W = window.innerWidth
  const out: string[] = []
  if (document.documentElement.scrollWidth > W + 1) out.push(`page ${document.documentElement.scrollWidth}`)
  for (const el of document.querySelectorAll('body *')) {
    const r = el.getBoundingClientRect()
    if (!r.width || !r.height || (r.left >= -1 && r.right <= W + 1)) continue
    const cs = getComputedStyle(el)
    if (cs.visibility === 'hidden' || cs.display === 'none') continue
    let a = el.parentElement
    let clipped = false
    while (a && a !== document.body) {
      const s = getComputedStyle(a)
      const ar = a.getBoundingClientRect()
      if (ar.left >= -1 && ar.right <= W + 1 && /(auto|scroll|hidden|clip)/.test(s.overflowX)) {
        clipped = true
        break
      }
      a = a.parentElement
    }
    if (!clipped) out.push(`${el.tagName.toLowerCase()}.${String(el.className).slice(0, 40)} [${Math.round(r.left)}, ${Math.round(r.right)}]`)
  }
  return out
}

const inWindow = async (page: Page, sel: ReturnType<Page['locator']>) => {
  const b = (await sel.boundingBox())!
  const v = page.viewportSize()!
  expect(b.x).toBeGreaterThanOrEqual(0)
  expect(b.y).toBeGreaterThanOrEqual(0)
  expect(b.x + b.width).toBeLessThanOrEqual(v.width + 0.5)
  expect(b.y + b.height).toBeLessThanOrEqual(v.height + 0.5)
}

for (const width of [375, 1440]) {
  test(`round 5 editor at ${width}: lineup, search, folders, drag, logo — inside the window`, async ({ browser, browserName, vaultURL }) => {
    test.skip(browserName !== 'chromium' && width < 700, 'mobile Chromium: the phone layout')
    const page = await open(browser, vaultURL, width)
    await page.goto('/tv/manage')
    // the list row shows the channel's logo
    await expect(page.locator('[data-channel-logo] img').first()).toBeVisible()
    await page.getByRole('button', { name: 'Кино', exact: true }).click()
    await page.getByRole('menuitem', { name: 'Изменить' }).click()
    const dialog = page.getByRole('dialog')
    const lineup = dialog.getByRole('list', { name: 'Состав канала' })
    await expect(lineup.getByRole('listitem')).toHaveCount(4)
    await expect(dialog.getByText('Серия · S2 E1 · Тот, где…')).toBeVisible()
    await expect(dialog.getByText('Логотип')).toBeVisible()

    // search and add
    await dialog.getByRole('searchbox', { name: 'Фильм, сериал или серия' }).fill('дру')
    await dialog.getByRole('button', { name: 'Добавить: Семь дорожек' }).click()
    await expect(lineup.getByRole('listitem')).toHaveCount(5)
    // folders
    await dialog.getByRole('radio', { name: 'Папки' }).click({ force: true })
    await dialog.getByRole('button', { name: 'Открыть: series' }).click()
    await dialog.getByRole('button', { name: 'Добавить: Season 01' }).click()
    await expect(lineup.getByRole('listitem')).toHaveCount(6)

    // drag the last pick to the top by its handle
    const handle = dialog.getByRole('button', { name: 'Переместить: Season 01' })
    await handle.scrollIntoViewIfNeeded()
    const first = lineup.getByRole('listitem').first()
    await first.scrollIntoViewIfNeeded()
    const hb = (await handle.boundingBox())!
    const fb = (await first.boundingBox())!
    await page.mouse.move(hb.x + hb.width / 2, hb.y + hb.height / 2)
    await page.mouse.down()
    for (let i = 1; i <= 12; i++) await page.mouse.move(hb.x + hb.width / 2, hb.y + hb.height / 2 + ((fb.y + 4 - (hb.y + hb.height / 2)) * i) / 12)
    await page.mouse.up()
    await expect(lineup.getByRole('listitem').first()).toHaveAttribute('data-value', 'folder:series/Season 01')

    // a finger drags the same handle (real touch events through CDP)
    if (width < 700) {
      const cdp = await page.context().newCDPSession(page)
      const h2 = dialog.getByRole('button', { name: 'Переместить: Season 01' })
      const second = lineup.getByRole('listitem').nth(2)
      const b2 = (await h2.boundingBox())!
      const sb = (await second.boundingBox())!
      const x = b2.x + b2.width / 2
      const y0 = b2.y + b2.height / 2
      const y1 = sb.y + sb.height - 4
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y: y0 }] })
      for (let i = 1; i <= 12; i++) await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: y0 + ((y1 - y0) * i) / 12 }] })
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
      await expect(lineup.getByRole('listitem').nth(2)).toHaveAttribute('data-value', 'folder:series/Season 01')
    }

    expect(await page.evaluate(outside)).toEqual([])
    await inWindow(page, dialog)
    await page.context().close()
  })

  test(`round 5 player at ${width}: 8 voices and 6 subtitles, the menu inside the window`, async ({ browser, browserName, vaultURL }) => {
    test.skip(browserName !== 'chromium' && width < 700, 'mobile Chromium: the phone layout')
    const page = await open(browser, vaultURL, width)
    await page.goto('/tv/1')
    await expect(page.getByTestId('live-player').locator('[data-channel-logo] img').first()).toBeVisible()
    await page.getByRole('button', { name: 'Звук и субтитры' }).click()
    const menu = page.getByRole('menu', { name: 'Звук и субтитры' })
    await expect(menu.getByRole('menuitemradio')).toHaveCount(8 + 1 + 6)
    await inWindow(page, menu)
    // the last subtitle is reachable inside the menu's own scroller
    const last = menu.getByRole('menuitemradio', { name: 'Deutsch' })
    await last.scrollIntoViewIfNeeded()
    await inWindow(page, last)
    await last.click()
    expect(await page.evaluate(outside)).toEqual([])
    await page.context().close()
  })
}
