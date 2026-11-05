// Reference screenshots of «Музыка», «Альбомы», the album page and the mini
// player, against bin/vault with the generated music library. The two real
// albums are joined by a few artwork-less tiles (the gradient fallback) so the
// grids show their real density. Opt-in:
//   SCREENSHOTS=1 npx playwright test e2e/screenshots-music.spec.ts --project chromium
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Browser, Page } from '@playwright/test'
import { MUSIC } from './music-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const OUT = path.join(HERE, '..', '..', 'docs', 'screenshots')
const shot = (page: Page, name: string) =>
  page.screenshot({ path: path.join(OUT, `music-${name}.jpg`), type: 'jpeg', quality: 85 })

test.skip(!process.env.SCREENSHOTS, 'set SCREENSHOTS=1 to refresh docs/screenshots')
test.use({ library: 'music' })
test.describe.configure({ timeout: 180_000 })

const VIEWPORTS = [
  { tag: 'desktop', width: 1440, height: 900, deviceScaleFactor: 2, isMobile: false },
  { tag: 'phone', width: 390, height: 844, deviceScaleFactor: 3, isMobile: true },
] as const

const FILLERS = [
  ['Ночной город', 'Северное сияние', 2019],
  ['Тёплые волны', 'Морской бриз', 2021],
  ['Летопись', 'Оркестр «Рассвет»', 2015],
  ['Электричество', 'Квартет имени лета', 2023],
  ['Дорога домой', 'Белые ночи', 2008],
  ['Сны о главном', 'Тихий берег', 2012],
  ['Акустика', 'Ветер с юга', 1999],
  ['Полярная звезда', 'Хор «Лира»', 2024],
] as const

const GENRES = ['Джаз', 'Электроника', 'Классика', 'Хип-хоп', 'Фолк', 'Блюз', 'Инди', 'Метал', 'Саундтреки', 'Шансон']

function fillers(start: number) {
  return FILLERS.map(([title, subtitle, year], i) => ({
    id: 900 + start + i,
    kind: 'album',
    title,
    subtitle,
    year,
    poster: '',
    backdrop: '',
    logo: '',
    progress: 0,
    onServer: true,
  }))
}

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
  })
  const page = await ctx.newPage()
  await page.route(/\/api\/music\/home$/, async (route) => {
    const res = await route.fetch()
    const json = (await res.json()) as { shelves: { id: string; cards: unknown[] }[] }
    for (const s of json.shelves) if (s.id === 'recent-albums') s.cards = [...s.cards, ...fillers(0)]
    await route.fulfill({ response: res, json })
  })
  // The two fixture genres plus a few more, so the «Жанры» row shows its density.
  await page.route(/\/api\/music\/genres$/, async (route) => {
    const res = await route.fetch()
    const json = (await res.json()) as { genres: { name: string; albums: number; songs: number }[] }
    json.genres = [...json.genres, ...GENRES.map((name) => ({ name, albums: 1, songs: 1 }))]
    await route.fulfill({ response: res, json })
  })
  await page.route(/\/api\/music\/albums(\?.*)?$/, async (route) => {
    const res = await route.fetch()
    const json = (await res.json()) as { items: unknown[]; total: number }
    const offset = Number(new URL(route.request().url()).searchParams.get('offset') || 0)
    if (offset === 0) {
      json.items = [...json.items, ...fillers(20)]
      json.total = json.items.length
    }
    await route.fulfill({ response: res, json })
  })
  return page
}

test('music screenshots', async ({ browser, baseURL, page: first }) => {
  await setupAdmin(first.request)
  await scanLibrary(first.request, (s) => s.albums >= 2)
  const cookies = await first.context().cookies()
  const albums = (await (await first.request.get('/api/music/albums?sort=title')).json()) as { items: { id: number; title: string }[] }
  const album = albums.items.find((a) => a.title === MUSIC.album)!.id
  const compilation = albums.items.find((a) => a.title === MUSIC.compilation)!.id

  for (const v of VIEWPORTS) {
    const page = await newPage(browser, baseURL!, v)
    await page.context().addCookies(cookies)

    await page.goto('/music')
    await expect(page.getByRole('heading', { name: 'Музыка', level: 1 })).toBeVisible()
    await page.waitForLoadState('networkidle')
    await page.waitForTimeout(300)
    await shot(page, `home-${v.tag}`)

    await page.goto('/music/albums')
    await expect(page.getByRole('heading', { name: 'Альбомы', level: 1 })).toBeVisible()
    await page.waitForLoadState('networkidle')
    await page.waitForTimeout(300)
    await shot(page, `albums-${v.tag}`)

    await page.goto(`/music/album/${album}`)
    await expect(page.getByRole('heading', { name: MUSIC.album, level: 1 })).toBeVisible()
    await page.waitForLoadState('networkidle')
    await page.waitForTimeout(300)
    await shot(page, `album-${v.tag}`)

    // Mini player: the compilation (long title and artist) playing, over
    // its album page with the current track marked.
    await page.goto(`/music/album/${compilation}`)
    await expect(page.getByRole('heading', { name: MUSIC.compilation, level: 1 })).toBeVisible()
    await page.getByRole('button', { name: new RegExp(MUSIC.hits[0]!.title) }).click()
    const mini = page.getByRole('complementary', { name: 'Сейчас играет' })
    await expect(mini).toBeVisible()
    await expect
      .poll(() => page.locator('audio[data-vault-audio][data-active]').evaluate((el: HTMLAudioElement) => el.currentTime), { timeout: 10_000 })
      .toBeGreaterThan(2)
    if (!v.isMobile) await page.mouse.move(v.width - 10, 10)
    await page.waitForTimeout(300)
    await shot(page, `mini-player-${v.tag}`)
    // and over another section
    await page.getByRole('navigation', { name: 'Разделы' }).getByRole('link', { name: 'Фильмы' }).click()
    await expect(page).toHaveURL(/\/movies$/)
    await page.waitForLoadState('networkidle')
    await page.waitForTimeout(300)
    await shot(page, `mini-player-movies-${v.tag}`)
    // A genre chip shuffles that genre: the mini player over the home.
    await page.getByRole('navigation', { name: 'Разделы' }).getByRole('link', { name: 'Музыка' }).click()
    await page.getByRole('region', { name: 'Жанры' }).getByRole('button', { name: 'Перемешать «Рок»' }).click()
    await expect(mini).toContainText(MUSIC.artist)
    await page.waitForLoadState('networkidle')
    if (!v.isMobile) await page.mouse.move(v.width - 10, 10)
    await page.waitForTimeout(300)
    await shot(page, `home-genres-${v.tag}`)
    await page.close()
  }
})

// «Сейчас играет» (cover, lyrics, queue), an artist, a playlist and the
// app password sheet. The playlist mosaic gets four distinct fixture
// covers (every generated album shares one image).
const IMAGES = path.join(HERE, 'fixtures', 'images')
const MOSAIC = ['africa-poster.jpg', 'sevenworlds-poster.jpg', 'interstellar-poster.jpg', 'wolf-poster.jpg'].map((f) => `/e2e-art/${f}`)
const shotAs = (page: Page, name: string) => page.screenshot({ path: path.join(OUT, `${name}.jpg`), type: 'jpeg', quality: 85 })

test('music full player screenshots', async ({ browser, baseURL, page: first }) => {
  await setupAdmin(first.request)
  await scanLibrary(first.request, (s) => s.albums >= 2)
  const cookies = await first.context().cookies()
  const albums = (await (await first.request.get('/api/music/albums?sort=title')).json()) as { items: { id: number; title: string }[] }
  const albumId = albums.items.find((a) => a.title === MUSIC.album)!.id
  const compId = albums.items.find((a) => a.title === MUSIC.compilation)!.id
  type Detail = { artist: { id: number }; tracks: { id: number }[] }
  const album = (await (await first.request.get(`/api/music/albums/${albumId}`)).json()) as Detail
  const comp = (await (await first.request.get(`/api/music/albums/${compId}`)).json()) as Detail
  const pl = (await (
    await first.request.post('/api/music/playlists', {
      data: { name: 'Вечерний плейлист', trackIds: [...album.tracks.map((t) => t.id), ...comp.tracks.map((t) => t.id)] },
    })
  ).json()) as { id: number }

  for (const v of VIEWPORTS) {
    const page = await newPage(browser, baseURL!, v)
    await page.context().addCookies(cookies)
    await page.route(/\/e2e-art\//, (route) => route.fulfill({ path: path.join(IMAGES, path.basename(new URL(route.request().url()).pathname)) }))
    await page.route(/\/api\/music\/playlists\/\d+$/, async (route) => {
      if (route.request().method() !== 'GET') return route.continue()
      const res = await route.fetch()
      const json = (await res.json()) as { playlist: { covers: string[] } }
      json.playlist.covers = MOSAIC
      await route.fulfill({ response: res, json })
    })
    const settle = async () => {
      await page.waitForLoadState('networkidle')
      if (!v.isMobile) await page.mouse.move(v.width - 10, 10)
      await page.waitForTimeout(400)
    }

    // the artist and the playlist
    await page.goto(`/music/artist/${album.artist.id}`)
    await expect(page.getByRole('heading', { level: 1, name: MUSIC.artist })).toBeVisible()
    await settle()
    await shot(page, `artist-${v.tag}`)

    await page.goto(`/music/playlist/${pl.id}`)
    await expect(page.getByRole('heading', { level: 1, name: 'Вечерний плейлист' })).toBeVisible()
    await settle()
    await shot(page, `playlist-${v.tag}`)

    // «Сейчас играет»: the album's first track (it has synced lyrics)
    await page.goto(`/music/album/${albumId}`)
    await page.getByRole('button', { name: 'Слушать' }).click()
    const mini = page.getByRole('complementary', { name: 'Сейчас играет' })
    await mini.getByRole('button', { name: /Открыть плеер/ }).click()
    const full = page.getByRole('dialog', { name: 'Сейчас играет' })
    await expect(full).toBeVisible()
    await expect
      .poll(() => page.locator('audio[data-vault-audio][data-active]').evaluate((el: HTMLAudioElement) => el.currentTime), { timeout: 10_000 })
      .toBeGreaterThan(MUSIC.lyrics[1]![0] + 0.5)
    await settle()
    await shot(page, `now-playing-${v.tag}`)

    await full.getByRole('button', { name: 'Текст' }).click()
    await expect(full.getByRole('button', { name: MUSIC.lyrics[1]![1] })).toHaveAttribute('aria-current', 'true')
    await settle()
    await shot(page, `now-playing-lyrics-${v.tag}`)

    // the queue: the playlist (long names from the compilation)
    await page.keyboard.press('Escape')
    await expect(full).toHaveCount(0)
    await page.goto(`/music/playlist/${pl.id}`)
    await page.getByRole('button', { name: 'Слушать' }).click()
    await mini.getByRole('button', { name: /Открыть плеер/ }).click()
    await full.getByRole('button', { name: 'Очередь' }).click()
    await expect(full.getByRole('list', { name: 'Далее' })).toBeVisible()
    await settle()
    await shot(page, `queue-${v.tag}`)
    await page.keyboard.press('Escape')
    await expect(full).toHaveCount(0)

    // the app password sheet right after «Создать пароль»
    await page.getByRole('button', { name: /Профиль:/ }).click()
    await page.getByRole('menuitem', { name: 'Пароль для приложений…' }).click()
    const sheet = page.getByRole('dialog', { name: 'Пароль для приложений' })
    await sheet.getByRole('button', { name: /Создать (пароль|новый)/ }).click()
    const confirm = page.getByRole('alertdialog', { name: 'Создать новый пароль?' })
    if (await confirm.isVisible().catch(() => false)) await confirm.getByRole('button', { name: 'Создать' }).click()
    await expect(sheet.getByRole('textbox', { name: 'Пароль' })).toBeVisible()
    await settle()
    await shotAs(page, `profile-app-password-${v.tag}`)
    await page.close()
  }
})
