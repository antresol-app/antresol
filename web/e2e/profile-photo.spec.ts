// A profile's photo, end to end: set in «Профили → Изменить» (framed, sent with
// Save), shown in the list, the top bar and the sign-in picker, removed again;
// and the phone's form opens without a field focused (no keyboard).
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { setupAdmin, expect, test } from './vault'

const PHOTO = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures', 'images', 'wolf-poster.jpg')

test.use({ library: 'empty' })

test('a photo is set, shown everywhere the avatar is, and removed', async ({ page, browser, vaultURL }) => {
  await setupAdmin(page.request)
  await page.goto('/manage/profiles')
  await page.getByRole('button', { name: 'Изменить' }).first().click()
  await page.getByTestId('photo-input').setInputFiles(PHOTO)
  const done = page.getByRole('button', { name: 'Готово' })
  await expect(done).toBeEnabled()
  await done.click()
  await page.getByRole('button', { name: 'Сохранить' }).click()

  // the list's avatar is the photo, and the server serves a JPEG square
  const row = page.locator('ul img[src*="/photo"]').first()
  await expect(row).toHaveAttribute('src', /^\/api\/profiles\/\d+\/photo\?v=\d+$/)
  const src = (await row.getAttribute('src'))!
  const res = await page.request.get(src)
  expect(res.status()).toBe(200)
  expect(res.headers()['content-type']).toBe('image/jpeg')
  expect(res.headers()['cache-control']).toContain('immutable')
  // the top bar's avatar follows (the session is read again after the save)
  await expect(page.locator('header img[src*="/photo"]')).toBeVisible()

  // the sign-in picker draws it before anyone signs in
  const anon = await browser.newContext({ baseURL: vaultURL, locale: 'ru-RU' })
  const picker = await anon.newPage()
  await picker.goto('/profiles')
  await expect(picker.locator(`img[src="${src}"]`)).toBeVisible()
  await anon.close()

  // removed: the letter is back
  await page.getByRole('button', { name: 'Изменить' }).first().click()
  await page.getByRole('button', { name: 'Убрать фото' }).click()
  await page.getByRole('button', { name: 'Сохранить' }).click()
  await expect(page.locator('ul img[src*="/photo"]')).toHaveCount(0)
  expect((await page.request.get(src)).status()).toBe(404)
})

// «Настройки» of the profile itself: an adult sets and removes their own photo there
async function ownPhoto(page: import('@playwright/test').Page) {
  await page.goto('/settings')
  await page.getByTestId('photo-input').setInputFiles(PHOTO)
  const done = page.getByRole('button', { name: 'Готово' })
  await expect(done).toBeEnabled()
  await done.click()
  // applied at once: no Save; the top bar and the row show it
  await expect(page.locator('header img[src*="/photo"]')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Убрать фото' })).toBeVisible()
  await page.getByRole('button', { name: 'Убрать фото' }).click()
  await expect(page.locator('header img[src*="/photo"]')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Загрузить фото…' })).toBeVisible()
}

test('an administrator sets their own photo in their settings', async ({ page }) => {
  await setupAdmin(page.request)
  await ownPhoto(page)
})

test('another adult sets their own photo in their settings; a kid has no row', async ({ page, browser, vaultURL }) => {
  await setupAdmin(page.request)
  const lena = (await (await page.request.post('/api/users', { data: { name: 'Лена' } })).json()) as { id: number }
  const kid = (await (await page.request.post('/api/users', { data: { name: 'Маша', isKid: true, maxRating: '6+' } })).json()) as { id: number }
  for (const [id, isKid] of [[lena.id, false], [kid.id, true]] as const) {
    const ctx = await browser.newContext({ baseURL: vaultURL, locale: 'ru-RU' })
    const own = await ctx.newPage()
    expect((await own.request.post('/api/login', { data: { userId: id } })).ok()).toBeTruthy()
    if (isKid) {
      await own.goto('/settings')
      await expect(own.getByRole('heading', { name: 'Настройки', level: 1 })).toBeVisible()
      await expect(own.getByRole('button', { name: /фото/i })).toHaveCount(0)
      // and the server says no, too
      expect((await own.request.delete('/api/me/photo')).status()).toBe(403)
    } else {
      await ownPhoto(own)
    }
    await ctx.close()
  }
})

test.describe('on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })

  test('the profile form opens with nothing focused: no keyboard, no AutoFill bar', async ({ page }) => {
    await setupAdmin(page.request)
    await page.goto('/manage/profiles')
    await page.getByRole('button', { name: /^Действия:/ }).first().click()
    await page.getByRole('menuitem', { name: 'Изменить' }).click()
    const dialog = page.getByRole('dialog', { name: 'Изменить профиль' })
    await expect(dialog).toBeVisible()
    await expect(page.getByLabel('Имя')).not.toBeFocused()
    await expect(dialog).toBeFocused()
  })
})
