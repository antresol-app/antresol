// A finished title reads as finished at a glance (web/DESIGN.md, «A watched
// title»): its picture dimmed, the eye in the middle and no progress bar,
// not even a full one; a title in progress has the bar and its picture as it
// is. Against a real bin/vault: the grid of «Фильмы» (the one sample film in
// three states) and «Книги» (the server marks a book read to its end, as it
// does a film), wide and on a phone. WATCHED_SHOTS=<dir> also writes the
// pictures there.
import path from 'node:path'
import type { Locator, Page } from '@playwright/test'
import { expect, scanLibrary, setupAdmin, test } from './vault'

test.use({ library: 'all' })

const SHOTS = process.env.WATCHED_SHOTS
const shot = (page: Page, name: string) => (SHOTS ? page.screenshot({ path: path.join(SHOTS, `${name}.png`) }) : Promise.resolve(undefined))

/** The brightness the picture of a card is drawn at (1: as it is); a card without a picture dims its title. */
const brightness = (card: Locator) =>
  card.locator('img, span[aria-hidden="true"]').first().evaluate((el) => {
    const m = /brightness\(([\d.]+)\)/.exec(getComputedStyle(el).filter)
    return m ? Number(m[1]) : 1
  })

const WIDTHS = [
  { name: 'wide', width: 1440, height: 900 },
  { name: 'phone', width: 390, height: 844 },
] as const

for (const v of WIDTHS) {
  test(`the grid of films: watched, in progress and plain (${v.name})`, async ({ page }) => {
    await page.setViewportSize({ width: v.width, height: v.height })
    await setupAdmin(page.request)
    await scanLibrary(page.request)
    // one sample film in three states of the profile
    await page.route(/\/api\/items\?.*kind=movie/, async (route) => {
      const res = await route.fetch()
      const body = (await res.json()) as { items: Record<string, unknown>[]; total: number }
      const base = body.items[0]!
      const items = [
        { ...base, id: 9001, title: 'Просмотренный фильм', watched: true, progress: 1 },
        { ...base, id: 9002, title: 'Фильм в процессе', watched: false, progress: 0.4 },
        { ...base, id: 9003, title: 'Новый фильм', watched: false, progress: 0 },
      ]
      await route.fulfill({ response: res, json: { items, total: items.length } })
    })
    await page.goto('/movies')
    const watched = page.getByRole('link', { name: /Просмотренный фильм/ })
    const going = page.getByRole('link', { name: /Фильм в процессе/ })
    const plain = page.getByRole('link', { name: /Новый фильм/ })
    await expect(watched).toBeVisible()

    // watched: dimmed, the eye, no bar at all (the full one the server's progress 1 would draw)
    await expect(watched.getByRole('img', { name: 'Просмотрено' })).toBeVisible()
    await expect(watched.getByRole('progressbar')).toHaveCount(0)
    expect(await brightness(watched)).toBeCloseTo(0.5, 2)
    // the eye is in the middle of the picture and a quarter of its width (28 to 52 px)
    const picture = (await watched.locator('img').first().boundingBox())!
    const eye = (await watched.getByRole('img', { name: 'Просмотрено' }).boundingBox())!
    expect(Math.abs(eye.x + eye.width / 2 - (picture.x + picture.width / 2))).toBeLessThan(1.5)
    expect(Math.abs(eye.y + eye.height / 2 - (picture.y + picture.height / 2))).toBeLessThan(1.5)
    expect(eye.width).toBeGreaterThanOrEqual(27.5)
    expect(eye.width).toBeLessThanOrEqual(52.5)
    expect(eye.width).toBeLessThan(picture.width * 0.4)
    // in progress: the bar, the picture as it is
    await expect(going.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40')
    await expect(going.getByRole('img', { name: 'Просмотрено' })).toHaveCount(0)
    expect(await brightness(going)).toBe(1)
    // plain: neither
    await expect(plain.getByRole('progressbar')).toHaveCount(0)
    expect(await brightness(plain)).toBe(1)
    await shot(page, `movies-${v.name}`)
    // the pointer lifts the dimming so a chosen card still reads
    if (v.name === 'wide') {
      await watched.hover()
      await expect.poll(() => brightness(watched)).toBeCloseTo(0.85, 1)
    }
  })

  test(`books: the server marks a finished one, the page dims it (${v.name})`, async ({ page }) => {
    await page.setViewportSize({ width: v.width, height: v.height })
    await setupAdmin(page.request)
    await scanLibrary(page.request, (s) => s.books >= 3)
    const home = (await (await page.request.get('/api/books/home')).json()) as { shelves: { cards: { id: number; kind: string }[] }[] }
    const books = home.shelves.flatMap((s) => s.cards).filter((c) => c.kind === 'book')
    expect(books.length).toBeGreaterThanOrEqual(2)
    const [read, half] = [books[0]!, books[1]!]
    for (const [b, fraction] of [[read, 0.99], [half, 0.5]] as const) {
      const r = await page.request.put(`/api/progress/${b.id}`, { data: { locator: 'epubcfi(/6/4)', fraction } })
      expect(r.status()).toBe(204)
    }
    await page.goto('/books')
    await expect(page.getByRole('heading', { level: 1, name: 'Книги' })).toBeVisible()
    const readLink = page.locator(`a[href="/books/${read.id}"]`).first()
    const halfLink = page.locator(`a[href="/books/${half.id}"]`).first()
    await expect(readLink.getByRole('img', { name: 'Прочитано' })).toBeVisible()
    await expect(readLink.getByRole('progressbar')).toHaveCount(0)
    expect(await brightness(readLink)).toBeCloseTo(0.5, 2)
    await expect(halfLink.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '50')
    expect(await brightness(halfLink)).toBe(1)
    await shot(page, `books-${v.name}`)
  })
}

// The eye and the play glyph of an episode tile share the middle: the eye at
// rest, the glyph (and not the eye) while the pointer is on the tile.
test.describe('episode tiles', () => {
  test.use({ library: 'playback' })

  test('the play glyph takes the middle of a watched episode from the eye while hovered', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await setupAdmin(page.request)
    await scanLibrary(page.request, (s) => s.episodes >= 3)
    const list = (await (await page.request.get('/api/items?kind=series')).json()) as { items: { id: number }[] }
    const seriesId = list.items[0]!.id
    const detail = (await (await page.request.get(`/api/items/${seriesId}`)).json()) as { seasons: { episodes: { id: number }[] }[] }
    const [first] = detail.seasons[0]!.episodes
    expect((await page.request.post(`/api/items/${first!.id}/watched`, { data: { watched: true } })).ok()).toBeTruthy()
    await page.goto(`/item/${seriesId}`)
    const tile = page.getByRole('button', { name: /Смотреть: серия 1,.*просмотрено/ })
    await expect(tile).toBeVisible()
    await tile.scrollIntoViewIfNeeded()
    await page.mouse.move(0, 0)
    const eye = tile.getByRole('img', { name: 'Просмотрено' })
    const glyph = tile.locator('svg[data-icon="play"]').locator('..')
    const opacity = (l: Locator) => l.evaluate((el) => Number(getComputedStyle(el).opacity))
    await expect.poll(() => opacity(eye)).toBe(1)
    expect(await opacity(glyph)).toBe(0)
    await shot(page, 'episodes-watched-rest')
    await tile.hover()
    await expect.poll(() => opacity(glyph)).toBe(1)
    await expect.poll(() => opacity(eye)).toBe(0)
    await shot(page, 'episodes-watched-hover')
    // an unwatched episode keeps no eye at all
    await expect(page.getByRole('button', { name: /Смотреть: серия 2/ }).getByRole('img', { name: 'Просмотрено' })).toHaveCount(0)
  })
})
