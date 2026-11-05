// Manga and audiobooks against a real bin/vault with the generated library
// (comics-audio-media.ts): the RTL reader turns forward with ← and keeps
// its page, the ribbon scrolls, an audiobook resumes after a reload and the
// sleep timer arms.
import type { Page } from '@playwright/test'
import { COMICS_AUDIO } from './comics-audio-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

test.use({ library: 'books' })

async function ready(page: Page) {
  await setupAdmin(page.request)
  await scanLibrary(page.request, (s) => s.comics >= 2 && s.audiobooks >= 1)
}

async function idOf(page: Page, kind: 'comic' | 'audiobook', title: string): Promise<number> {
  const list = (await (await page.request.get(`/api/books?kind=${kind}`)).json()) as { items: { id: number; title: string }[] }
  const it = list.items.find((b) => b.title === title)
  expect(it, `${kind} «${title}» scanned`).toBeTruthy()
  return it!.id
}

const reader = (page: Page) => page.getByTestId('comic-reader')
const count = (page: Page) => reader(page).locator('[aria-live]')

/** The <audio> element that plays now and its position. */
const currentTime = (page: Page) =>
  page.evaluate(() => (document.querySelector('audio[data-vault-audio][data-active]') as HTMLAudioElement | null)?.currentTime ?? -1)

test('comic RTL: ArrowLeft goes forward and progress persists', async ({ page }) => {
  await ready(page)
  const id = await idOf(page, 'comic', COMICS_AUDIO.comic)
  await page.goto(`/books/comic/${id}`)
  await expect(page.getByRole('heading', { level: 1, name: COMICS_AUDIO.comic })).toBeVisible()
  await expect(page.getByText('2 тома')).toBeVisible()
  await expect(page.getByRole('list', { name: 'Тома' }).getByRole('listitem')).toHaveCount(2)
  await page.getByRole('link', { name: 'Читать' }).click()
  await expect(page).toHaveURL(/\/read\/\d+$/)
  await expect(reader(page)).toHaveAttribute('data-dir', 'rtl')
  await expect(count(page)).toHaveText('1 / 6')
  await expect(page.getByRole('img', { name: 'Страница 1' })).toBeVisible()

  await page.keyboard.press('ArrowLeft')
  await expect(count(page)).toHaveText('2 / 6')
  await page.keyboard.press('ArrowLeft')
  await expect(count(page)).toHaveText('3 / 6')
  await page.keyboard.press('ArrowRight')
  await expect(count(page)).toHaveText('2 / 6')
  await page.keyboard.press('ArrowLeft')
  await expect(count(page)).toHaveText('3 / 6')
  // the slider runs right to left
  await expect(page.getByRole('slider', { name: 'Страница' })).toHaveAttribute('dir', 'rtl')

  // turning hid the panels; a tap in the middle brings them back
  await expect(reader(page)).toHaveAttribute('data-hidden', '')
  const box = (await reader(page).boundingBox())!
  await page.mouse.click(box.width / 2, box.height / 2)
  await expect(reader(page)).not.toHaveAttribute('data-hidden', '')
  await page.getByRole('button', { name: 'Назад' }).click()
  await expect(page).toHaveURL(new RegExp(`/books/comic/${id}$`))
  const resume = page.getByRole('link', { name: 'Продолжить: Том 1' })
  await expect(resume).toBeVisible()
  await resume.click()
  await expect(count(page)).toHaveText('3 / 6')
})

test('ribbon mode scrolls vertically', async ({ page }) => {
  await ready(page)
  const id = await idOf(page, 'comic', COMICS_AUDIO.comic)
  const series = (await (await page.request.get(`/api/books/${id}`)).json()) as { volumes: { id: number }[] }
  await page.goto(`/read/${series.volumes[0]!.id}`)
  await expect(count(page)).toHaveText('1 / 6')
  // opening a volume is not reading it: nothing saved
  await page.waitForTimeout(2500)
  const opened = (await (await page.request.get(`/api/books/${series.volumes[0]!.id}`)).json()) as { progress: unknown }
  expect(opened.progress).toBeNull()
  await page.getByRole('button', { name: 'Вид' }).click()
  await page.getByRole('menuitemradio', { name: /Лента/ }).click()
  await expect(reader(page)).toHaveAttribute('data-view', 'ribbon')
  const ribbon = page.getByTestId('comic-ribbon')
  await expect(page.getByRole('img', { name: 'Страница 1' })).toBeVisible()
  const sizes = await ribbon.evaluate((el) => ({ scroll: el.scrollHeight, client: el.clientHeight, width: el.scrollWidth - el.clientWidth }))
  expect(sizes.scroll).toBeGreaterThan(sizes.client * 3)
  expect(sizes.width).toBe(0) // no sideways scroll
  await ribbon.evaluate((el) => el.scrollBy(0, el.clientHeight * 2))
  await expect.poll(() => ribbon.evaluate((el) => el.scrollTop)).toBeGreaterThan(0)
  await expect.poll(async () => Number(await reader(page).getAttribute('data-page'))).toBeGreaterThan(1)
  // the series remembers the ribbon
  await page.reload()
  await expect(reader(page)).toHaveAttribute('data-view', 'ribbon')
})

test('ribbon placeholders have the pages\' real shape (tall strip, saved page kept)', async ({ page }) => {
  await ready(page)
  const id = await idOf(page, 'comic', COMICS_AUDIO.comic)
  const series = (await (await page.request.get(`/api/books/${id}`)).json()) as { volumes: { id: number; fileId: number }[] }
  const vol2 = series.volumes[1]!
  const pages = (await (await page.request.get(`/api/comics/${vol2.fileId}/pages`)).json()) as { sizes: [number, number][] }
  expect(pages.sizes[2]).toEqual([375, 1653])
  expect((await page.request.put(`/api/progress/${vol2.id}`, { data: { locator: 'page:3', fraction: 0.6 } })).ok()).toBeTruthy()
  await page.addInitScript((sid) => localStorage.setItem(`vault.comic.view.${sid}`, 'ribbon'), id)
  // pages not loaded yet already have their size
  await page.route(/\/api\/comics\/\d+\/page\/\d+$/, async (route) => {
    await new Promise((r) => setTimeout(r, 800))
    await route.continue()
  })
  await page.goto(`/read/${vol2.id}`)
  await expect(reader(page)).toHaveAttribute('data-view', 'ribbon')
  const strip = page.getByTestId('comic-ribbon').locator('[data-page="3"]')
  const shape = await strip.evaluate((el) => el.clientHeight / el.clientWidth)
  expect(shape).toBeGreaterThan(4.3)
  expect(shape).toBeLessThan(4.5)
  await page.waitForLoadState('networkidle')
  await expect(reader(page)).toHaveAttribute('data-page', '3')
})

test('a missing volume says so without «Повторить»', async ({ page }) => {
  await ready(page)
  await page.goto('/read/999999')
  await expect(page.getByRole('heading', { name: 'Этот том недоступен' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Назад' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Повторить' })).toHaveCount(0)
})

test('the book speed holds across files and reloads', async ({ page }) => {
  await ready(page)
  const id = await idOf(page, 'audiobook', COMICS_AUDIO.audiobook)
  await page.goto(`/books/audiobook/${id}`)
  await page.getByRole('button', { name: 'Скорость: 1×' }).click()
  await page.getByRole('menuitemradio', { name: '1,5×' }).click()
  await page.getByRole('button', { name: 'Слушать', exact: true }).click()
  const rate = () => page.evaluate(() => (document.querySelector('audio[data-vault-audio][data-active]') as HTMLAudioElement | null)?.playbackRate ?? -1)
  await expect.poll(() => currentTime(page), { timeout: 15_000 }).toBeGreaterThan(1)
  await expect.poll(rate).toBe(1.5)
  // into the second file (not +30 s: the last 30 s of this 60 s book count as heard)
  await page.getByRole('list', { name: 'Главы' }).getByRole('button').nth(1).click()
  await expect(page.getByTestId('mini-player')).toContainText('Глава 2')
  await expect.poll(() => currentTime(page), { timeout: 15_000 }).toBeGreaterThan(2)
  await expect.poll(rate).toBe(1.5)
  await page.reload()
  await page.getByRole('button', { name: /^Продолжить · осталось/ }).click()
  await expect.poll(() => currentTime(page), { timeout: 15_000 }).toBeGreaterThan(0)
  await expect.poll(rate).toBe(1.5)
})

test('audiobook resumes after reload', async ({ page }) => {
  await ready(page)
  const id = await idOf(page, 'audiobook', COMICS_AUDIO.audiobook)
  await page.goto(`/books/audiobook/${id}`)
  await expect(page.getByRole('heading', { level: 1, name: COMICS_AUDIO.audiobook })).toBeVisible()
  await expect(page.getByText(`Читает: ${COMICS_AUDIO.narrator}`)).toBeVisible()
  const chapters = page.getByRole('list', { name: 'Главы' }).getByRole('button')
  await expect(chapters).toHaveCount(COMICS_AUDIO.files)
  await expect(chapters.first()).toContainText('Глава 1')
  await expect(chapters.first()).toContainText('0:20')

  await page.getByRole('button', { name: 'Слушать', exact: true }).click()
  const mini = page.getByTestId('mini-player')
  await expect(mini).toContainText(COMICS_AUDIO.audiobook)
  await expect(mini).toContainText('Глава 1')
  await expect(mini.getByRole('button', { name: '−15 с' })).toBeVisible()
  await expect(mini.getByRole('button', { name: '+30 с' })).toBeVisible()
  await expect.poll(() => currentTime(page), { timeout: 15_000 }).toBeGreaterThanOrEqual(5)
  await expect(chapters.first()).toHaveAttribute('aria-current', 'true')

  await page.reload()
  const resume = page.getByRole('button', { name: /^Продолжить · осталось \d+ мин$/ })
  await expect(resume).toBeVisible()
  await resume.click()
  await expect.poll(() => currentTime(page), { timeout: 10_000 }).toBeGreaterThanOrEqual(4)

  // +30 s crosses into the second file
  await page.getByTestId('mini-player').getByRole('button', { name: '+30 с' }).click()
  await expect(page.getByTestId('mini-player')).toContainText('Глава 2')
})

test('sleep menu arms 15 minutes', async ({ page }) => {
  await ready(page)
  const id = await idOf(page, 'audiobook', COMICS_AUDIO.audiobook)
  await page.goto(`/books/audiobook/${id}`)
  await page.getByRole('button', { name: 'Слушать', exact: true }).click()
  await expect.poll(() => currentTime(page), { timeout: 15_000 }).toBeGreaterThan(0)

  // in «Сейчас играет»
  await page.getByRole('button', { name: /Открыть плеер/ }).click()
  const np = page.getByRole('dialog', { name: 'Сейчас играет' })
  await expect(np.getByRole('button', { name: 'Скорость: 1×' })).toBeVisible()
  await np.getByRole('button', { name: 'Таймер сна' }).click()
  await page.getByRole('menuitem', { name: '15 мин' }).click()
  await expect(np.getByRole('button', { name: /^Таймер сна: осталось 1[45]:\d\d$/ })).toBeVisible()

  // speed 1,5× sticks to the element
  await np.getByRole('button', { name: 'Скорость: 1×' }).click()
  await page.getByRole('menuitemradio', { name: '1,5×' }).click()
  await expect(np.getByRole('button', { name: 'Скорость: 1,5×' })).toBeVisible()
  expect(await page.evaluate(() => (document.querySelector('audio[data-vault-audio][data-active]') as HTMLAudioElement).playbackRate)).toBe(1.5)

  // «Выключить»
  await np.getByRole('button', { name: /^Таймер сна: осталось/ }).click()
  await page.getByRole('menuitem', { name: 'Выключить' }).click()
  await expect(np.getByRole('button', { name: 'Таймер сна' })).toBeVisible()
})

test('«Сменить профиль» silences the book; the next profile gets no player and no progress', async ({ page }) => {
  test.setTimeout(60_000)
  await ready(page)
  expect((await page.request.post('/api/users', { data: { name: 'Маша', avatar: 'grad-3' } })).ok()).toBeTruthy()
  const id = await idOf(page, 'audiobook', COMICS_AUDIO.audiobook)
  await page.goto(`/books/audiobook/${id}`)
  await page.getByRole('button', { name: 'Слушать', exact: true }).click()
  await expect.poll(() => currentTime(page), { timeout: 15_000 }).toBeGreaterThan(3)

  let switched = false
  const putsAfter: string[] = []
  page.on('request', (r) => {
    if (switched && r.method() === 'PUT' && new URL(r.url()).pathname.startsWith('/api/progress/')) putsAfter.push(r.url())
  })
  await page.getByRole('button', { name: /^Профиль: / }).click()
  await page.getByRole('menuitem', { name: 'Сменить профиль' }).click()
  await page.waitForURL(/\/profiles$/)
  switched = true
  const silent = () =>
    page.evaluate(() =>
      [...document.querySelectorAll<HTMLAudioElement>('audio[data-vault-audio]')].every((a) => a.paused && !a.getAttribute('src')),
    )
  // silent already on «Кто смотрит?», where no player shows
  expect(await silent()).toBe(true)
  await page.getByRole('button', { name: /^Маша/ }).click()
  await expect(page.getByRole('navigation', { name: 'Разделы' })).toBeVisible()
  await expect(page.getByTestId('mini-player')).toHaveCount(0)
  // a ticker save would come within 10 s
  await page.waitForTimeout(11_000)
  expect(await silent()).toBe(true)
  await expect(page.getByTestId('mini-player')).toHaveCount(0)
  expect(putsAfter).toEqual([])
  const mine = (await (await page.request.get(`/api/books/${id}`)).json()) as { progress: unknown }
  expect(mine.progress).toBeNull()
})
