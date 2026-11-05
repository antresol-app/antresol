// Reference screenshots of «Сохранить офлайн»: an album page while it is
// being saved and once it is («Загружено»), the downloads list (/offline)
// and the app without the server. Against bin/vault with music and books,
// with the service worker. Opt-in:
//   SCREENSHOTS=1 npx playwright test e2e/screenshots-offline.spec.ts --project chromium
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Browser, Page } from '@playwright/test'
import { BOOKS } from './books-media'
import { COMICS_AUDIO } from './comics-audio-media'
import { MUSIC } from './music-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const OUT = path.join(HERE, '..', '..', 'docs', 'screenshots')
const shot = (page: Page, name: string) => page.screenshot({ path: path.join(OUT, `offline-${name}.jpg`), type: 'jpeg', quality: 85 })

test.skip(!process.env.SCREENSHOTS, 'set SCREENSHOTS=1 to refresh docs/screenshots')
test.use({ library: 'all', serviceWorkers: 'allow' })
test.describe.configure({ timeout: 180_000 })

const VIEWPORTS = [
  { tag: 'desktop', width: 1440, height: 900, deviceScaleFactor: 2, isMobile: false },
  { tag: 'phone', width: 390, height: 844, deviceScaleFactor: 3, isMobile: true },
] as const

async function newPage(browser: Browser, baseURL: string, v: (typeof VIEWPORTS)[number]) {
  const ctx = await browser.newContext({
    baseURL,
    viewport: { width: v.width, height: v.height },
    deviceScaleFactor: v.deviceScaleFactor,
    isMobile: v.isMobile,
    hasTouch: v.isMobile,
    colorScheme: 'dark',
    locale: 'ru-RU',
    reducedMotion: 'reduce',
    serviceWorkers: 'allow',
  })
  return ctx.newPage()
}

async function saveHere(page: Page) {
  await page.getByRole('button', { name: 'На это устройство' }).click()
  await page.getByRole('menuitem', { name: /Сохранить офлайн/ }).click()
}

test('offline screenshots', async ({ browser, baseURL, page: first }) => {
  await setupAdmin(first.request)
  await scanLibrary(first.request, (s) => s.albums >= 2 && s.books >= 3 && s.audiobooks >= 1 && s.comics >= 1)
  const cookies = await first.context().cookies()
  const albums = (await (await first.request.get('/api/music/albums?sort=title')).json()) as { items: { id: number; title: string }[] }
  const album = albums.items.find((a) => a.title === MUSIC.album)!.id
  type Page_ = { items: { id: number; title: string }[] }
  const byTitle = async (kind: string, title: string) =>
    ((await (await first.request.get(`/api/books?kind=${kind}&limit=100`)).json()) as Page_).items.find((b) => b.title === title)!.id
  const book = await byTitle('book', BOOKS.epub)
  const audiobook = await byTitle('audiobook', COMICS_AUDIO.audiobook)
  const comic = await byTitle('comic', COMICS_AUDIO.comic)

  for (const v of VIEWPORTS) {
    const page = await newPage(browser, baseURL!, v)
    await page.context().addCookies(cookies)
    const settle = async (idle = true) => {
      if (idle) await page.waitForLoadState('networkidle', { timeout: 10_000 }).catch(() => undefined)
      if (!v.isMobile) await page.mouse.move(v.width - 10, 10)
      await page.waitForTimeout(400)
    }
    await page.goto('/')
    await expect.poll(() => page.evaluate(() => !!navigator.serviceWorker?.controller), { timeout: 15_000 }).toBe(true)

    // the album while it saves: the second track waits until the shot is taken
    let release!: () => void
    const gate = new Promise<void>((r) => (release = r))
    let n = 0
    await page.route(/\/api\/tracks\/\d+\/stream\?accept=/, async (route) => {
      if (++n === 2) await gate
      await route.continue()
    })
    await page.goto(`/music/album/${album}`)
    await expect(page.getByRole('heading', { level: 1, name: MUSIC.album })).toBeVisible()
    await saveHere(page)
    await expect(page.getByRole('menu')).toBeHidden()
    await expect(page.getByRole('status').filter({ hasText: /Сохраняется… [1-9]\d* %/ })).toBeVisible()
    await settle(false) // a track download is held open
    await shot(page, `saving-${v.tag}`)
    release()
    await expect(page.getByRole('link', { name: /Загружено/ })).toBeVisible({ timeout: 20_000 })
    await settle()
    await shot(page, `saved-album-${v.tag}`)
    await page.unrouteAll({ behavior: 'ignoreErrors' })

    // a book, an audiobook and the manga too
    for (const [url, title] of [
      [`/books/${book}`, BOOKS.epub],
      [`/books/audiobook/${audiobook}`, COMICS_AUDIO.audiobook],
      [`/books/comic/${comic}`, COMICS_AUDIO.comic],
    ] as const) {
      await page.goto(url)
      await expect(page.getByRole('heading', { level: 1, name: title })).toBeVisible()
      await saveHere(page)
      await expect(page.getByRole('link', { name: /Загружено/ })).toBeVisible({ timeout: 20_000 })
    }

    // the downloads list
    await page.goto('/offline')
    await expect(page.getByRole('heading', { level: 1, name: 'На этом устройстве' })).toBeVisible()
    await expect(page.getByText(/Сохранено: /)).toBeVisible()
    await settle()
    await shot(page, `downloads-${v.tag}`)

    // no server: what is saved, and the album playing from the device
    await page.context().setOffline(true)
    await page.reload()
    await expect(page.getByText('Нет связи с сервером — доступно сохранённое на этом устройстве')).toBeVisible()
    await settle()
    await shot(page, `home-${v.tag}`)
    await page.getByRole('button', { name: `Слушать: ${MUSIC.album}` }).click()
    await expect
      .poll(() => page.locator('audio[data-vault-audio][data-active]').evaluate((el: HTMLAudioElement) => el.currentTime), { timeout: 10_000 })
      .toBeGreaterThan(1)
    await settle()
    await shot(page, `home-playing-${v.tag}`)
    await page.context().close()
  }
})
