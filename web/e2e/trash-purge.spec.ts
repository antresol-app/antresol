// Deleting for good, against a real bin/vault:
// - «Корзина» on a phone: a row's menu deletes one item, «⋯ → Выбрать» picks
//   several for the bar under the list, «⋯ → Очистить корзину…» empties it;
//   each confirmation names what goes and how much it frees;
// - «Удалить навсегда, минуя корзину» in the confirmations that send files to
//   the trash («Файлы» ⌘⌫, Управление → Файлы): unchecked by default, checked
//   it says the delete cannot be undone and nothing is left in the trash.
import type { APIRequestContext, Page } from '@playwright/test'
import { expect, LIBRARY_MOVIE, scanLibrary, setupAdmin, test } from './vault'

test.describe.configure({ timeout: 60_000 })

const MOVIE_DIR = `${LIBRARY_MOVIE.title} (${LIBRARY_MOVIE.year})`

type TrashItem = { id: number; name: string; size: number }
const trashList = async (request: APIRequestContext) => (await (await request.get('/api/files/trash')).json()) as TrashItem[]

/** Empty folders made and moved to the trash through the API, plus the movie's poster (4 КБ-ish, a known size). */
async function fillTrash(request: APIRequestContext, names: string[]) {
  for (const name of names) {
    const r = await request.post('/api/files/mkdir', { data: { root: 'movies', path: '', name } })
    expect(r.ok(), await r.text()).toBeTruthy()
  }
  const r = await request.post('/api/files/trash', { data: { root: 'movies', paths: names, confirm: true } })
  expect(r.ok(), await r.text()).toBeTruthy()
}

const sheet = (page: Page) => page.getByRole('alertdialog')

test.describe('phone «Корзина»', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true })

  test('delete one, delete the picked ones, empty the trash', async ({ page }) => {
    await setupAdmin(page.request)
    await scanLibrary(page.request)
    await fillTrash(page.request, ['Папка А', 'Папка Б', 'Папка В', 'Папка Г'])
    // a file of a known size: the movie itself (4096 bytes)
    const video = `${MOVIE_DIR}/Interstellar.${LIBRARY_MOVIE.year}.1080p.BluRay.mkv`
    expect((await page.request.post('/api/files/trash', { data: { root: 'movies', paths: [video], confirm: true } })).ok()).toBeTruthy()
    expect(await trashList(page.request)).toHaveLength(5)

    await page.goto('/files/trash')
    await expect(page.getByRole('heading', { level: 1, name: 'Корзина' })).toBeVisible()
    const list = page.getByRole('list', { name: 'Корзина' })
    await expect(list.getByRole('listitem')).toHaveCount(5)

    // one row: a tap opens its menu; the confirmation names it and its size
    await list.getByRole('button', { name: /Interstellar/ }).click()
    await page.getByRole('menuitem', { name: 'Удалить навсегда…' }).click()
    await expect(sheet(page)).toContainText('Interstellar')
    await expect(sheet(page)).toContainText(/4\s?КБ/)
    await expect(sheet(page)).toContainText('нельзя отменить')
    await sheet(page).getByRole('button', { name: 'Удалить' }).click()
    await expect(list.getByRole('listitem')).toHaveCount(4)

    // pick two through «⋯ → Выбрать», delete them from the bar under the list
    await page.getByRole('button', { name: 'Действия с корзиной' }).click()
    await page.getByRole('menuitem', { name: 'Выбрать' }).click()
    await list.getByRole('button', { name: 'Папка А' }).click()
    await list.getByRole('button', { name: 'Папка Б' }).click()
    await expect(list.getByRole('button', { name: 'Папка А' })).toHaveAttribute('aria-pressed', 'true')
    const bar = page.getByRole('toolbar', { name: 'Выбрано 2 из 4' })
    await expect(bar).toBeVisible()
    await bar.getByRole('button', { name: 'Удалить' }).click()
    await expect(sheet(page)).toContainText('2 объекта')
    await sheet(page).getByRole('button', { name: 'Удалить' }).click()
    await expect(list.getByRole('listitem')).toHaveCount(2)
    await expect(list.getByRole('button', { name: 'Папка А' })).toHaveCount(0)

    // «⋯ → Очистить корзину…»
    await page.getByRole('button', { name: 'Действия с корзиной' }).click()
    await page.getByRole('menuitem', { name: 'Очистить корзину…' }).click()
    await expect(sheet(page)).toContainText('2 объекта')
    await sheet(page).getByRole('button', { name: 'Очистить' }).click()
    await expect(page.getByText('Корзина пуста')).toBeVisible()
    expect(await trashList(page.request)).toHaveLength(0)
  })
})

test('«Файлы» ⌘⌫: «Удалить навсегда, минуя корзину» skips the trash', async ({ page }) => {
  await setupAdmin(page.request)
  for (const name of ['Оставить', 'Стереть']) {
    expect((await page.request.post('/api/files/mkdir', { data: { root: 'movies', path: '', name } })).ok()).toBeTruthy()
  }
  await page.goto('/files/movies')
  const option = (name: string) => page.getByRole('option', { name, exact: true })

  // unchecked by default: to the trash
  await option('Оставить').click()
  await page.keyboard.press('ControlOrMeta+Backspace')
  const skip = sheet(page).getByRole('checkbox', { name: 'Удалить навсегда, минуя корзину' })
  await expect(skip).not.toBeChecked()
  await sheet(page).getByRole('button', { name: 'В корзину' }).click()
  await expect(option('Оставить')).toHaveCount(0)
  expect((await trashList(page.request)).map((t) => t.name)).toEqual(['Оставить'])

  // checked: the text says it cannot be undone, nothing reaches the trash
  await option('Стереть').click()
  await page.keyboard.press('ControlOrMeta+Backspace')
  await sheet(page).getByRole('checkbox', { name: 'Удалить навсегда, минуя корзину' }).check()
  await expect(sheet(page)).toContainText('нельзя отменить')
  await sheet(page).getByRole('button', { name: 'Удалить' }).click()
  await expect(option('Стереть')).toHaveCount(0)
  await expect(page.getByRole('status').filter({ hasText: 'удалено навсегда' })).toBeVisible()
  expect((await trashList(page.request)).map((t) => t.name)).toEqual(['Оставить'])
})

test('Управление → Файлы: a title deleted permanently leaves nothing in the trash', async ({ page }) => {
  await setupAdmin(page.request)
  await scanLibrary(page.request)
  await page.goto('/manage/files')
  await page.getByRole('button', { name: `Удалить «${LIBRARY_MOVIE.title}»` }).click()
  await expect(sheet(page)).toContainText('Файлы попадут в Корзину')
  const skip = sheet(page).getByRole('checkbox', { name: 'Удалить навсегда, минуя корзину' })
  await expect(skip).not.toBeChecked()
  await skip.check()
  await expect(sheet(page)).toContainText('нельзя отменить')
  await sheet(page).getByRole('button', { name: 'Удалить' }).click()
  await expect(page.getByText(`«${LIBRARY_MOVIE.title}» удалено навсегда.`)).toBeVisible()
  expect(await trashList(page.request)).toHaveLength(0)
})
