// «Файлы» against a real bin/vault with a generated media root (files-media.ts):
// browse Фильмы → folder, thumbnails and sizes, Quick Look, Свойства with the
// library link, rename as admin, trash and restore, a regular profile's menu
// and the phone layout.
import type { APIRequestContext, Page } from '@playwright/test'
import { FILES } from './files-media'
import { ADMIN, expect, setupAdmin, test } from './vault'

test.use({ library: 'files' })
test.describe.configure({ timeout: 60_000 })

async function ready(request: APIRequestContext) {
  await setupAdmin(request)
  await expect
    .poll(async () => ((await (await request.get('/api/items?kind=movie')).json()) as { total: number }).total, { timeout: 20_000 })
    .toBeGreaterThanOrEqual(2)
}

const option = (page: Page, name: string) => page.getByRole('option', { name, exact: true })

test('admin: Фильмы → folder → thumbnails, Quick Look, Свойства, rename', async ({ page }) => {
  await ready(page.request)
  await page.goto('/')
  await page.getByRole('button', { name: `Профиль: ${ADMIN.name}` }).click()
  await page.getByRole('menuitem', { name: 'Файлы' }).click()
  await expect(page).toHaveURL(/\/files\/movies$/)
  await expect(page.getByRole('navigation', { name: 'Места' }).getByRole('link', { name: 'Фильмы' })).toHaveAttribute('aria-current', 'page')

  // The movie folder shows its poster.
  const folder = option(page, FILES.movie.folder)
  await expect(folder.locator('[data-thumb="loaded"]')).toBeVisible({ timeout: 15_000 })
  await folder.dblclick()
  await expect(page).toHaveURL(new RegExp(`/files/movies/${encodeURIComponent(FILES.movie.folder).replace(/[()]/g, '\\$&')}$`))
  await expect(page.getByRole('heading', { level: 1, name: FILES.movie.folder })).toBeVisible()

  // A frame of the video as its icon.
  const video = option(page, FILES.movie.video)
  await expect(video.locator('[data-thumb="loaded"]')).toBeVisible({ timeout: 15_000 })
  const thumbOk = await video.locator('img').evaluate((img: HTMLImageElement) => img.naturalWidth > 0)
  expect(thumbOk).toBe(true)

  // List view: sizes.
  await page.getByRole('radio', { name: 'Список' }).click()
  const row = page.getByRole('row', { name: FILES.movie.video, exact: true })
  await expect(row.getByRole('gridcell').nth(2)).toHaveText(/\d+(,\d+)? (КБ|МБ)/)
  await expect(page.getByRole('row', { name: FILES.movie.srt, exact: true }).getByRole('gridcell').nth(3)).toHaveText('Субтитры SRT')

  // Space → Quick Look of the subtitles; ← goes to the neighbour, Esc closes.
  await page.getByRole('row', { name: FILES.movie.srt, exact: true }).click()
  await page.keyboard.press('Space')
  const ql = page.getByRole('dialog', { name: /Быстрый просмотр/ })
  await expect(ql).toBeVisible()
  await expect(ql.getByText('Привет из субтитров')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(ql).toBeHidden()

  // ⌘I → Свойства of the video: the library item and the media details.
  await row.click()
  await page.keyboard.press('ControlOrMeta+i')
  const info = page.getByRole('complementary', { name: 'Свойства' })
  await expect(info.getByRole('link', { name: new RegExp(`Фильм в медиатеке.*${FILES.movie.title}`) })).toBeVisible()
  await expect(info.getByText('Длительность')).toBeVisible({ timeout: 15_000 })
  await expect(info.getByText(/640×360/)).toBeVisible()

  // Back to Фильмы: the folder we came from is selected; rename it.
  await page.getByRole('button', { name: 'Назад', exact: true }).click()
  await expect(page).toHaveURL(/\/files\/movies$/)
  const folderRow = page.getByRole('row', { name: FILES.movie.folder, exact: true })
  await expect(folderRow).toHaveAttribute('aria-selected', 'true')
  await folderRow.click({ button: 'right' })
  await page.getByRole('menuitem', { name: /Переименовать/ }).click()
  const sheet = page.getByRole('dialog', { name: 'Переименовать' })
  await sheet.getByRole('textbox', { name: 'Новое имя' }).fill(FILES.renamed)
  await sheet.getByRole('button', { name: 'Переименовать' }).click()
  await expect(page.getByRole('row', { name: FILES.renamed, exact: true })).toBeVisible()
  await expect(page.getByRole('row', { name: FILES.movie.folder, exact: true })).toHaveCount(0)
  await expect(page.getByRole('row', { name: FILES.renamed, exact: true })).toHaveAttribute('aria-selected', 'true')
})

test('admin: move to the trash with ⌘⌫ and restore from «Корзина»', async ({ page }) => {
  await ready(page.request)
  await page.goto(`/files/movies/${encodeURIComponent(FILES.movie.folder)}`)
  await option(page, FILES.movie.srt).click()
  await page.keyboard.press('ControlOrMeta+Backspace')
  const sheet = page.getByRole('alertdialog', { name: /в корзину\?/ })
  await sheet.getByRole('button', { name: 'В корзину' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'в корзине' })).toBeVisible()
  await expect(option(page, FILES.movie.srt)).toHaveCount(0)

  await page.getByRole('link', { name: 'Корзина' }).click()
  const row = page.getByRole('row', { name: FILES.movie.srt, exact: true })
  await expect(row).toContainText(`Фильмы › ${FILES.movie.folder}`)
  await row.click()
  await page.getByRole('button', { name: 'Восстановить' }).click()
  await expect(page.getByText('Корзина пуста')).toBeVisible()

  await page.goBack()
  await expect(option(page, FILES.movie.srt)).toBeVisible()
})

test('a regular profile can browse but not change files', async ({ page }) => {
  await ready(page.request)
  const r = await page.request.post('/api/users', { data: { name: 'Маша', avatar: 'grad-3' } })
  expect(r.ok(), await r.text()).toBeTruthy()
  const { id } = (await r.json()) as { id: number }
  await page.request.post('/api/logout')
  expect((await page.request.post('/api/login', { data: { userId: id } })).ok()).toBeTruthy()

  await page.goto('/files/movies')
  await expect(page.getByRole('link', { name: 'Корзина' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Новая папка' })).toHaveCount(0)
  await option(page, FILES.movie.folder).click({ button: 'right' })
  const menu = page.getByRole('menu')
  await expect(menu.getByRole('menuitem', { name: /^Открыть/ })).toBeVisible()
  await expect(menu.getByRole('menuitem', { name: /Свойства/ })).toBeVisible()
  await expect(menu.getByRole('menuitem', { name: /Переименовать/ })).toHaveCount(0)
  await expect(menu.getByRole('menuitem', { name: /корзину/ })).toHaveCount(0)
  // The trash is not reachable by URL either.
  await page.keyboard.press('Escape')
  await page.goto('/files/trash')
  await expect(page).toHaveURL(/\/files\/movies$/)
})

test.describe('phone', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true })

  test('iOS «Файлы»: Обзор → Фильмы → folder; «⋯» opens the action sheet', async ({ page }) => {
    await ready(page.request)
    await page.goto('/files')
    await expect(page.getByRole('heading', { level: 1, name: 'Обзор' })).toBeVisible()
    await page.getByRole('navigation', { name: 'Места' }).getByRole('link', { name: 'Фильмы' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'Фильмы' })).toBeVisible()
    await page.getByRole('link', { name: 'Обзор' }).waitFor()
    // Long press on an icon opens its action sheet (no right-click on iOS).
    const tile = page.getByRole('option', { name: FILES.movie.folder, exact: true })
    const box = (await tile.boundingBox())!
    const at = { pointerType: 'touch', isPrimary: true, bubbles: true, clientX: box.x + box.width / 2, clientY: box.y + box.height / 3 }
    await tile.dispatchEvent('pointerdown', at)
    await page.waitForTimeout(700)
    await tile.dispatchEvent('pointerup', at)
    const held = page.getByRole('menu', { name: FILES.movie.folder })
    await expect(held.getByRole('menuitem', { name: 'Свойства' })).toBeVisible()
    await page.getByRole('button', { name: 'Отменить' }).click()
    await expect(held).toBeHidden()
    await expect(page.getByRole('heading', { level: 1, name: 'Фильмы' })).toBeVisible()
    // Icons by default; switch to the list through «⋯».
    await page.getByRole('button', { name: 'Вид и сортировка' }).click()
    await page.getByRole('menuitemradio', { name: 'Список' }).click()
    await page.getByRole('row', { name: FILES.movie.folder, exact: true }).click()
    await expect(page.getByRole('heading', { level: 1, name: FILES.movie.folder })).toBeVisible()
    await page.getByRole('button', { name: `Действия: ${FILES.movie.video}` }).click()
    const sheet = page.getByRole('menu', { name: FILES.movie.video })
    await expect(sheet.getByRole('menuitem', { name: 'Скачать на это устройство' })).toBeVisible()
    await sheet.getByRole('menuitem', { name: 'Быстрый просмотр' }).click()
    await expect(page.getByRole('dialog', { name: /Быстрый просмотр/ })).toBeVisible()
  })
})
