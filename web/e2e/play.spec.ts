// Real playback against bin/vault with generated videos (see media.ts):
// direct play, HLS for a codec the browser cannot take, multi-part movies,
// «Следующая серия», subtitles, the transcode fallback and progress on Главная.
import type { Page } from '@playwright/test'
import { PLAYBACK } from './media'
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'playback' })
test.describe.configure({ timeout: 90_000 })

interface ListCard {
  id: number
  title: string
}

async function ready(page: Page) {
  await setupAdmin(page.request)
  // The startup scan indexes the library; wait until everything is there.
  await expect
    .poll(
      async () => {
        const m = (await (await page.request.get('/api/items?kind=movie')).json()) as { total: number }
        const s = (await (await page.request.get('/api/items?kind=series')).json()) as { total: number }
        return m.total >= 3 && s.total >= 1
      },
      { timeout: 20_000 },
    )
    .toBe(true)
}

async function itemId(page: Page, kind: 'movie' | 'series', title: string): Promise<number> {
  const r = (await (await page.request.get(`/api/items?kind=${kind}&limit=50`)).json()) as { items: ListCard[] }
  const hit = r.items.find((c) => c.title === title)
  expect(hit, `${title} in /api/items`).toBeTruthy()
  return hit!.id
}

const video = (page: Page) => page.getByTestId('player').locator('video')
const currentTime = (page: Page) => video(page).evaluate((v: HTMLVideoElement) => v.currentTime)

async function expectPlaying(page: Page, min = 1) {
  await expect(page.getByTestId('player')).toBeVisible()
  await page.waitForTimeout(3000)
  await expect.poll(() => currentTime(page), { timeout: 20_000 }).toBeGreaterThan(min)
}

test('direct play → progress → «Продолжить» on Главная', async ({ page }) => {
  await ready(page)
  await page.goto('/movies')
  await page.getByRole('link', { name: new RegExp(PLAYBACK.direct.title) }).click()
  await expect(page.getByRole('heading', { name: PLAYBACK.direct.title, level: 1 })).toBeVisible()
  await expect(page.getByText('12+', { exact: true }).first()).toBeVisible()
  await page.getByRole('button', { name: 'Смотреть', exact: true }).click()

  await expect(page).toHaveURL(/\/watch\/\d+$/)
  await expectPlaying(page)
  await expect(page.getByTestId('player')).toHaveAttribute('data-mode', 'direct')
  // → skips 10 s forward (twice), so there is something to resume.
  await page.keyboard.press('ArrowRight')
  await page.keyboard.press('ArrowRight')
  await expect.poll(() => currentTime(page)).toBeGreaterThan(20)

  await page.getByRole('button', { name: 'Закрыть плеер' }).click()
  await expect(page).toHaveURL(/\/item\/\d+$/)
  // The item page now offers to continue.
  await expect(page.getByRole('button', { name: /^Продолжить с 0:[23]\d$/ })).toBeVisible()

  await page.getByRole('navigation', { name: 'Разделы' }).getByRole('link', { name: 'Главная', exact: true }).click()
  const cont = page.getByRole('region', { name: 'Продолжить' })
  const card = cont.getByRole('link', { name: new RegExp(PLAYBACK.direct.title) })
  await expect(card).toBeVisible()
  await expect(card.getByRole('progressbar')).toHaveAttribute('aria-valuenow', /^[1-9]\d?$/)
})

test('HEVC/AC-3 MKV plays through HLS from the hero, and the session is closed on exit', async ({ page }) => {
  await ready(page)
  await page.goto('/')
  const hero = page.getByRole('region', { name: 'Рекомендуем' })
  await hero.getByRole('button', { name: new RegExp(`: ${PLAYBACK.hls.title}$`) }).click()
  await expect(hero.getByRole('group', { name: new RegExp(PLAYBACK.hls.title) })).toBeVisible()
  await hero.getByRole('button', { name: 'Смотреть' }).click()

  await expect(page).toHaveURL(/\/watch\/\d+$/)
  await expectPlaying(page)
  await expect(page.getByTestId('player')).toHaveAttribute('data-mode', /^(remux|transcode)$/)

  const stopped = page.waitForRequest((r) => r.method() === 'DELETE' && /\/api\/hls\/[^/]+$/.test(r.url()))
  await page.keyboard.press('Escape')
  await stopped
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByRole('region', { name: 'Продолжить' }).getByRole('link', { name: new RegExp(PLAYBACK.hls.title) })).toBeVisible()
})

test('a failed direct play retries once with transcode-only caps', async ({ page }) => {
  await ready(page)
  const id = await itemId(page, 'movie', PLAYBACK.direct.title)
  const bodies: { caps: { videoCodecs: string[]; containers: string[] } }[] = []
  await page.route('**/api/play/*', async (route) => {
    bodies.push(route.request().postDataJSON())
    if (bodies.length === 1) {
      // Pretend the server chose direct play of something the browser cannot open.
      const real = await route.fetch()
      const json = await real.json()
      await route.fulfill({ response: real, json: { ...json, mode: 'direct', url: '/api/health' } })
    } else await route.continue()
  })
  await page.goto(`/watch/${id}`)
  await expectPlaying(page)
  expect(bodies).toHaveLength(2)
  expect(bodies[1]!.caps.containers).toEqual([])
  expect(bodies[1]!.caps.videoCodecs).toEqual(['h264'])
  await expect(page.getByTestId('player')).toHaveAttribute('data-mode', /^(remux|transcode)$/)
})

test('the quality menu restarts at the same place', async ({ page }) => {
  await ready(page)
  const id = await itemId(page, 'movie', PLAYBACK.direct.title)
  const bodies: { quality?: string; startSec?: number }[] = []
  page.on('request', (r) => {
    if (r.method() === 'POST' && /\/api\/play\/\d+$/.test(r.url())) bodies.push(r.postDataJSON())
  })
  await page.goto(`/watch/${id}`)
  await expectPlaying(page, 3)
  await page.getByTestId('player').hover()
  await page.getByRole('button', { name: /^Качество: Авто/ }).click()
  await page.getByRole('menuitemradio', { name: /480p/ }).click()
  await expect.poll(() => bodies.length).toBe(2)
  expect(bodies[0]!.quality).toBe('auto')
  expect(bodies[1]!.quality).toBe('480')
  expect(bodies[1]!.startSec).toBeGreaterThan(2)
  // The 360p sample is already under 480p: it keeps playing as is, from there.
  await expect(page.getByRole('button', { name: 'Качество: 480p' })).toBeAttached()
  await expect.poll(() => currentTime(page), { timeout: 20_000 }).toBeGreaterThan(bodies[1]!.startSec! + 0.5)
})

test('an open HLS session gets a heartbeat (a long pause keeps it)', async ({ page }) => {
  await ready(page)
  const id = await itemId(page, 'movie', PLAYBACK.hls.title)
  await page.goto(`/watch/${id}`)
  await expect(page.getByTestId('player')).toHaveAttribute('data-mode', /^(remux|transcode)$/, { timeout: 20_000 })
  await page.getByRole('button', { name: 'Пауза' }).click()
  const beat = await page.waitForRequest((r) => r.method() === 'PUT' && /\/api\/hls\/[0-9a-f]{16}$/.test(r.url()), { timeout: 25_000 })
  expect((await beat.response())?.status()).toBe(200)
})

async function progressOf(page: Page, id: number): Promise<number> {
  return ((await (await page.request.get(`/api/items/${id}`)).json()) as { progress: number }).progress
}

test('HLS resume from 0:20 saves the real position on close', async ({ page }) => {
  await ready(page)
  const id = await itemId(page, 'movie', PLAYBACK.hls.title)
  await page.goto(`/watch/${id}?t=20`)
  await expect(page.getByTestId('player')).toHaveAttribute('data-mode', /^(remux|transcode)$/, { timeout: 20_000 })
  // Stream time restarts at 0 for an HLS session that starts mid-file.
  await expect.poll(() => currentTime(page), { timeout: 20_000 }).toBeGreaterThan(1)
  await page.getByRole('button', { name: 'Закрыть плеер' }).click()
  await expect(page).toHaveURL(/\/item\/\d+$/)
  const p = await progressOf(page, id)
  expect(p * PLAYBACK.hls.seconds).toBeGreaterThan(20)
  expect(p).toBeLessThan(0.92)
})

test('finishing the last part marks the movie watched', async ({ page }) => {
  await ready(page)
  const id = await itemId(page, 'movie', PLAYBACK.parts.title)
  await page.goto(`/watch/${id}?t=${PLAYBACK.parts.seconds * 2 - 3}`)
  await expect(page.getByText('Часть 2 из 2')).toBeVisible({ timeout: 20_000 })
  // The end of the movie closes the player.
  await expect(page).toHaveURL(/\/item\/\d+$/, { timeout: 25_000 })
  expect(await progressOf(page, id)).toBe(1)
})

test('multi-part movie continues into part 2', async ({ page }) => {
  await ready(page)
  const id = await itemId(page, 'movie', PLAYBACK.parts.title)
  await page.goto(`/item/${id}`)
  await expect(page.getByText('2 части, воспроизводятся подряд')).toBeVisible()
  await page.goto(`/watch/${id}?t=${PLAYBACK.parts.seconds - 3}`)
  await expect(page.getByText('Часть 1 из 2')).toBeVisible()
  await expect(page.getByTestId('player')).toHaveAttribute('data-part', '2', { timeout: 20_000 })
  await expect(page.getByText('Часть 2 из 2')).toBeVisible()
  await expect.poll(() => currentTime(page), { timeout: 20_000 }).toBeGreaterThan(0.5)
})

test('series: episode list and «Следующая серия»', async ({ page }) => {
  await ready(page)
  const sid = await itemId(page, 'series', PLAYBACK.series.title)
  await page.goto(`/item/${sid}`)
  await expect(page.getByRole('button', { name: 'Смотреть С1 Э1' })).toBeVisible()
  const list = page.getByRole('region', { name: 'Серии' })
  await expect(list.getByRole('button', { name: /серия 2, Горы/ })).toBeVisible()
  await list.getByRole('button', { name: /серия 1, Острова/ }).click()
  await expect(page).toHaveURL(/\/watch\/\d+$/)
  const first = page.url()
  await expect(page.getByText('С1 Э1 · Острова')).toBeVisible()
  await expectPlaying(page, 0.5)
  // Jump close to the end: the card appears, then the next episode starts by itself.
  await page.getByRole('slider', { name: 'Позиция' }).focus()
  for (let i = 0; i < 2; i++) await page.keyboard.press('ArrowRight')
  const upNext = page.getByRole('complementary', { name: 'Следующая серия' })
  await expect(upNext).toBeVisible()
  await expect(upNext.getByText('Горы')).toBeVisible()
  await expect(page).not.toHaveURL(first, { timeout: 25_000 })
  await expect(page.getByText('С1 Э2 · Горы')).toBeVisible()
})

test('subtitles and the track menu', async ({ page }) => {
  await ready(page)
  const id = await itemId(page, 'movie', PLAYBACK.direct.title)
  await page.goto(`/watch/${id}`)
  await expectPlaying(page, 0.5)
  await page.getByRole('button', { name: 'Аудио и субтитры' }).click()
  const menu = page.getByRole('menu', { name: 'Аудио и субтитры' })
  await expect(menu.getByRole('menuitemradio', { name: 'Выключены' })).toHaveAttribute('aria-checked', 'true')
  await menu.getByRole('menuitemradio', { name: /Русский/ }).click()
  await expect(page.getByText('Привет из субтитров')).toBeVisible()
  // The choice is remembered for the next video.
  await page.reload()
  await expect(page.getByText('Привет из субтитров')).toBeVisible({ timeout: 20_000 })
})

test('the subtitle search sheet owns the keyboard; Esc closes only the sheet', async ({ page }) => {
  await ready(page)
  const id = await itemId(page, 'movie', PLAYBACK.direct.title)
  // no OpenSubtitles key on the e2e server: the results and the download are faked
  await page.route(/\/api\/subtitles\/\d+\/search\?/, (r) =>
    r.fulfill({ json: { results: [{ subtitleId: 7, lang: 'ru', release: 'Fake.Release.ru', downloads: 12, hashMatch: true }] } }),
  )
  await page.route(/\/api\/subtitles\/\d+$/, (r) => r.fulfill({ status: 204, body: '' }))
  await page.goto(`/watch/${id}`)
  await expectPlaying(page, 0.5)
  await page.getByRole('button', { name: 'Аудио и субтитры' }).click()
  await page.getByRole('menuitem', { name: 'Найти субтитры…' }).click()
  const sheet = page.getByRole('dialog', { name: 'Найти субтитры' })
  await expect(sheet.getByText('Совпадает с файлом')).toBeVisible()

  // focus is in the sheet: Space does not reach the player
  await expect.poll(() => sheet.evaluate((el) => el.contains(document.activeElement))).toBe(true)
  await page.keyboard.press('Space')
  await expect.poll(() => video(page).evaluate((v: HTMLVideoElement) => v.paused)).toBe(false)

  // the download worked but the track list cannot be refreshed: the sheet stays with a message
  await page.route(/\/api\/items\/\d+$/, (r) => r.fulfill({ status: 500, json: { error: 'Ошибка сервера' } }))
  await sheet.getByRole('button', { name: /Скачать/ }).click()
  await expect(sheet.getByRole('alert')).toContainText('Субтитры скачаны, но список дорожек не обновился')

  await page.keyboard.press('Escape')
  await expect(sheet).toBeHidden()
  await expect(page.getByTestId('player')).toBeVisible()
  await expect(page).toHaveURL(new RegExp(`/watch/${id}`))
  // a click on the dimmed area closes the sheet, not the player
  await page.getByRole('button', { name: 'Аудио и субтитры' }).click()
  await page.getByRole('menuitem', { name: 'Найти субтитры…' }).click()
  await expect(sheet).toBeVisible()
  await page.mouse.click(8, 450)
  await expect(sheet).toBeHidden()
  await expect(page.getByTestId('player')).toBeVisible()
  // with the sheet gone, Esc closes the player again
  await page.keyboard.press('Escape')
  await expect(page.getByTestId('player')).toBeHidden()
})
