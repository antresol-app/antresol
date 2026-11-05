// Music against a real bin/vault with generated FLAC albums (music-media.ts):
// the album page starts the queue, the mini player and the sound survive
// navigation, and a video pauses the music.
import type { Page } from '@playwright/test'
import { MUSIC } from './music-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

test.use({ library: 'music' })

/** currentTime of the <audio> element that is playing now. */
const audioTime = (page: Page) =>
  page.locator('audio[data-vault-audio][data-active]').evaluate((el: HTMLAudioElement) => el.currentTime)
const audioPaused = (page: Page) =>
  page.locator('audio[data-vault-audio][data-active]').evaluate((el: HTMLAudioElement) => el.paused)

async function playAlbum(page: Page) {
  await setupAdmin(page.request)
  await scanLibrary(page.request, (s) => s.albums >= 2 && s.movies > 0)
  await page.goto('/')
  const nav = page.getByRole('navigation', { name: 'Разделы' })
  await nav.getByRole('link', { name: 'Музыка' }).click()
  await expect(page).toHaveURL(/\/music$/)
  await expect(page.getByRole('heading', { level: 1, name: 'Музыка' })).toBeVisible()
  await page.getByRole('link', { name: new RegExp(MUSIC.album) }).first().click()
  await expect(page).toHaveURL(/\/music\/album\/\d+$/)
  await expect(page.getByRole('heading', { level: 1, name: MUSIC.album })).toBeVisible()
  await expect(page.getByText('Рок · 1988 · Lossless')).toBeVisible()
  await expect(page.getByText('3 песни, 1 мин')).toBeVisible()
  await page.getByRole('button', { name: 'Слушать' }).click()
  const mini = page.getByRole('complementary', { name: 'Сейчас играет' })
  await expect(mini).toContainText(MUSIC.tracks[0]!)
  await expect(mini).toContainText(MUSIC.artist)
  return mini
}

test('album plays and mini player survives navigation', async ({ page }) => {
  const mini = await playAlbum(page)
  // the sound actually runs
  await expect.poll(() => audioTime(page), { timeout: 10_000 }).toBeGreaterThan(1)
  await expect(mini.getByRole('button', { name: 'Пауза' })).toBeVisible()
  // the current track is marked in the list
  await expect(page.getByRole('main').getByRole('button', { name: new RegExp(MUSIC.tracks[0]!) })).toHaveAttribute('aria-current', 'true')

  // another section: the capsule stays and time keeps going
  await page.getByRole('navigation', { name: 'Разделы' }).getByRole('link', { name: 'Фильмы' }).click()
  await expect(page).toHaveURL(/\/movies$/)
  await expect(mini).toBeVisible()
  await expect(page.locator('main')).toHaveAttribute('data-mini', 'on')
  const t = await audioTime(page)
  await expect.poll(() => audioTime(page), { timeout: 7_000 }).toBeGreaterThan(t + 0.5)

  // «Следующий трек» moves the queue on
  await mini.getByRole('button', { name: 'Следующий трек' }).click()
  await expect(mini).toContainText(MUSIC.tracks[1]!)
  // pause from the capsule
  await mini.getByRole('button', { name: 'Пауза' }).click()
  await expect(mini.getByRole('button', { name: 'Играть' })).toBeVisible()
  await expect.poll(() => audioPaused(page)).toBe(true)
})

test('opening a video pauses music', async ({ page }) => {
  await playAlbum(page)
  await expect.poll(() => audioTime(page), { timeout: 10_000 }).toBeGreaterThan(0.5)
  await page.getByRole('navigation', { name: 'Разделы' }).getByRole('link', { name: 'Главная' }).click()
  await page.getByRole('region', { name: 'Рекомендуем' }).getByRole('button', { name: 'Смотреть' }).click()
  await expect(page).toHaveURL(/\/watch\/\d+/)
  await expect.poll(() => audioPaused(page)).toBe(true)
  // back in the app the capsule offers to resume
  await page.goBack()
  const play = page.getByRole('complementary', { name: 'Сейчас играет' }).getByRole('button', { name: 'Играть' })
  await expect(play).toBeVisible()
  // resuming takes the lock screen and media keys back from the video
  await play.click()
  await expect.poll(() => audioPaused(page)).toBe(false)
  const title = () => page.evaluate(() => ('mediaSession' in navigator ? (navigator.mediaSession.metadata?.title ?? '') : 'unsupported'))
  await expect.poll(title).toMatch(new RegExp(`^(${MUSIC.tracks[0]}|unsupported)$`))
})

test('now playing opens, shows queue and lyrics', async ({ page }) => {
  const mini = await playAlbum(page)
  await mini.getByRole('button', { name: /Открыть плеер/ }).click()
  const full = page.getByRole('dialog', { name: 'Сейчас играет' })
  await expect(full).toBeVisible()
  await expect(full.getByRole('heading', { name: MUSIC.tracks[0]! })).toBeVisible()
  await expect(full.getByText('Lossless')).toBeVisible()
  await expect(full.getByRole('link', { name: MUSIC.artist })).toBeVisible()

  // synced lyrics follow the song; a click on a line seeks there
  await full.getByRole('button', { name: 'Текст' }).click()
  await expect(full.getByRole('button', { name: MUSIC.lyrics[0]![1] })).toBeVisible()
  await full.getByRole('button', { name: MUSIC.lyrics[2]![1] }).click()
  await expect.poll(() => audioTime(page), { timeout: 7_000 }).toBeGreaterThan(MUSIC.lyrics[2]![0] - 0.5)
  await expect(full.getByRole('button', { name: MUSIC.lyrics[2]![1] })).toHaveAttribute('aria-current', 'true')

  // the queue: what plays now and what comes next
  await full.getByRole('button', { name: 'Очередь' }).click()
  const next = full.getByRole('list', { name: 'Далее' })
  await expect(next).toContainText(MUSIC.tracks[1]!)
  await expect(next).toContainText(MUSIC.tracks[2]!)
  await next.getByRole('button', { name: `Ниже: ${MUSIC.tracks[1]!}` }).click()
  // focus follows the moved track
  await expect(next.getByRole('button', { name: `Выше: ${MUSIC.tracks[1]!}` })).toBeFocused()
  await expect(next.getByRole('listitem').first()).toContainText(MUSIC.tracks[2]!)
  await next.getByRole('button', { name: new RegExp(`${MUSIC.tracks[1]!}.*Включить`) }).click()
  await expect(full.getByRole('heading', { name: MUSIC.tracks[1]! })).toBeVisible()

  // repeat cycles, Esc closes; the mini player is still there
  const repeat = full.getByRole('button', { name: 'Повтор: выкл' })
  await repeat.click()
  await expect(full.getByRole('button', { name: 'Повтор: все' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(full).toHaveCount(0)
  await expect(mini).toContainText(MUSIC.tracks[1]!)
})

test('back closes «Сейчас играет» together with the page under it', async ({ page }) => {
  const mini = await playAlbum(page)
  const album = page.url()
  await page.getByRole('link', { name: MUSIC.artist }).first().click()
  await expect(page).not.toHaveURL(album)
  await mini.getByRole('button', { name: /Открыть плеер/ }).click()
  const full = page.getByRole('dialog', { name: 'Сейчас играет' })
  await expect(full).toBeVisible()
  await page.goBack()
  await expect(page).toHaveURL(album)
  await expect(full).toHaveCount(0)
  expect(await page.evaluate(() => getComputedStyle(document.body).overflow)).not.toBe('hidden')
  await expect(mini).toContainText(MUSIC.tracks[0]!)
})

test('create playlist from track menu and play it', async ({ page }) => {
  await playAlbum(page)
  const row = page.getByRole('main').getByRole('listitem').filter({ hasText: MUSIC.tracks[1]! })
  await row.hover()
  await row.getByRole('button', { name: 'Ещё' }).click()
  await page.getByRole('menuitem', { name: 'Добавить в плейлист…' }).click()
  const sheet = page.getByRole('dialog', { name: 'Добавить в плейлист' })
  await sheet.getByRole('button', { name: 'Новый плейлист…' }).click()
  await page.getByLabel('Название').fill('Вечер')
  await page.getByRole('button', { name: 'Создать' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)

  await page.goto('/music/playlists')
  await page.getByRole('link', { name: /Вечер/ }).click()
  await expect(page).toHaveURL(/\/music\/playlist\/\d+$/)
  await expect(page.getByRole('heading', { level: 1, name: 'Вечер' })).toBeVisible()
  await expect(page.getByText(/^1 песня/)).toBeVisible()
  await page.getByRole('button', { name: 'Слушать' }).click()
  const mini = page.getByRole('complementary', { name: 'Сейчас играет' })
  await expect(mini).toContainText(MUSIC.tracks[1]!)
  await expect.poll(() => audioTime(page), { timeout: 10_000 }).toBeGreaterThan(0.5)
})

test('shuffle library and genre', async ({ page }) => {
  await setupAdmin(page.request)
  await scanLibrary(page.request, (s) => s.albums >= 2 && s.movies > 0)
  await page.goto('/music')
  await expect(page.getByRole('heading', { level: 1, name: 'Музыка' })).toBeVisible()
  const mini = page.getByRole('complementary', { name: 'Сейчас играет' })
  const any = [...MUSIC.tracks, ...MUSIC.hits.map((h) => h.title)]

  // «Перемешать все»: some track of the library plays
  await page.getByRole('button', { name: 'Перемешать все' }).click()
  await expect(mini).toContainText(new RegExp(any.join('|')))
  await expect.poll(() => audioPaused(page)).toBe(false)

  // «Жанры» → «Рок»: a track of «Первый альбом» (the only rock album)
  const genres = page.getByRole('region', { name: 'Жанры' })
  await expect(genres.getByRole('button', { name: 'Перемешать «Поп»' })).toBeVisible()
  const rock = page.waitForResponse((r) => r.url().includes('/api/music/shuffle?source=genre'))
  await genres.getByRole('button', { name: 'Перемешать «Рок»' }).click()
  const { tracks } = (await (await rock).json()) as { tracks: { album: string }[] }
  expect(tracks.length).toBe(MUSIC.tracks.length)
  expect(tracks.every((t) => t.album === MUSIC.album)).toBe(true)
  await expect(mini).toContainText(MUSIC.artist)
  await expect(mini).toContainText(new RegExp(MUSIC.tracks.join('|')))

  // the artist page: «Перемешать» plays the artist's catalogue
  await mini.getByRole('button', { name: 'Пауза' }).click()
  await page.goto('/music/artists')
  await page.getByRole('link', { name: new RegExp(MUSIC.artist) }).first().click()
  await expect(page.getByRole('heading', { level: 1, name: MUSIC.artist })).toBeVisible()
  await page.getByRole('button', { name: 'Перемешать' }).click()
  await expect(mini.getByRole('button', { name: 'Пауза' })).toBeVisible()
  await expect(mini).toContainText(new RegExp(MUSIC.tracks.join('|')))
  await expect.poll(() => audioPaused(page)).toBe(false)
})

test('app password sheet shows password', async ({ page, baseURL }) => {
  await setupAdmin(page.request)
  await page.goto('/')
  await page.getByRole('button', { name: /Профиль:/ }).click()
  await page.getByRole('menuitem', { name: 'Пароль для приложений…' }).click()
  const sheet = page.getByRole('dialog', { name: 'Пароль для приложений' })
  await expect(sheet).toContainText(`сервер — ${new URL(baseURL!).origin}`)
  await expect(sheet).toContainText(`Каталог книг: ${new URL(baseURL!).origin}/opds`)
  await sheet.getByRole('button', { name: 'Создать пароль' }).click()
  const field = sheet.getByRole('textbox', { name: 'Пароль' })
  await expect(field).toHaveValue(/.{8,}/)
  const password = await field.inputValue()
  await expect(sheet.getByRole('button', { name: 'Скопировать пароль' })).toBeVisible()
  // the password really signs a Subsonic app in
  const ping = await page.request.get(`/rest/ping.view?u=${encodeURIComponent('Александр')}&p=${encodeURIComponent(password)}&v=1.16.1&c=e2e&f=json`)
  expect(((await ping.json()) as { 'subsonic-response': { status: string } })['subsonic-response'].status).toBe('ok')
  // reopened: the password is not shown again
  await sheet.getByRole('button', { name: 'Закрыть' }).click()
  await page.getByRole('button', { name: /Профиль:/ }).click()
  await page.getByRole('menuitem', { name: 'Пароль для приложений…' }).click()
  await expect(sheet.getByRole('button', { name: 'Создать новый' })).toBeVisible()
  await expect(sheet.getByRole('textbox', { name: 'Пароль' })).toHaveCount(0)
})

test.describe('library search', () => {
  test.use({ library: 'music', fakes: false })

  test('search finds seeded album and song', async ({ page }) => {
    await setupAdmin(page.request)
    await scanLibrary(page.request, (s) => s.albums >= 2 && s.tracks >= 3)
    await page.goto(`/search?q=${encodeURIComponent(MUSIC.album)}`)
    // the album's own title: «Лучшее совпадение»
    const best = page.getByRole('region', { name: 'Лучшее совпадение' })
    await expect(best.getByRole('link', { name: new RegExp(MUSIC.album) })).toHaveAttribute('href', /\/music\/album\/\d+$/)
    // the best match is not listed again under its kind (pages/Search.tsx)
    await expect(page.getByRole('region', { name: 'Альбомы' }).getByRole('link', { name: new RegExp(MUSIC.album) })).toHaveCount(0)

    await page.getByRole('searchbox', { name: 'Фильмы, музыка и книги' }).fill(MUSIC.tracks[1]!)
    const songs = page.getByRole('region', { name: 'Песни' })
    await expect(songs).toContainText(`${MUSIC.artist} · ${MUSIC.album}`)
    await songs.getByRole('button', { name: new RegExp(MUSIC.tracks[1]!) }).click()
    const mini = page.getByRole('complementary', { name: 'Сейчас играет' })
    await expect(mini).toContainText(MUSIC.tracks[1]!)
  })
})
