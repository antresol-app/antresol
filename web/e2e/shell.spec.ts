// Shell, sign-in and Главная against a real bin/vault with a real (tiny) media
// library. Every test runs on its own fresh server (see vault.ts).
import { ADMIN, LIBRARY_MOVIE, expect, scanLibrary, setupAdmin, test } from './vault'
import type { Page } from '@playwright/test'

async function signInFromPicker(page: Page) {
  await page.goto('/profiles')
  await page.getByRole('button', { name: new RegExp(ADMIN.name) }).click()
  await page.getByLabel('Пароль', { exact: true }).fill(ADMIN.password)
  await page.getByRole('button', { name: 'Войти' }).click()
  await expect(page.getByRole('navigation', { name: 'Разделы' })).toBeVisible()
}

test('first run → profile picker → Главная with the scanned library', async ({ page }) => {
  // First run: the welcome screen validates, creates the administrator and signs in.
  await page.goto('/')
  await expect(page).toHaveURL(/\/welcome$/)
  await expect(page.getByRole('heading', { name: 'Добро пожаловать в Antresol' })).toBeVisible()
  await page.getByRole('button', { name: 'Продолжить' }).click()
  await expect(page.getByText('Как вас зовут?')).toBeVisible()
  await page.getByLabel('Ваше имя').fill(ADMIN.name)
  await page.getByLabel('Пароль', { exact: true }).fill(ADMIN.password)
  await page.getByRole('button', { name: 'Продолжить' }).click()
  await expect(page).toHaveURL(/\/$/)

  // Index the test library through the real API.
  await scanLibrary(page.request)

  // Switch profile → «Кто смотрит?».
  await page.getByRole('button', { name: `Профиль: ${ADMIN.name}` }).click()
  await expect(page.getByRole('menuitem', { name: 'Управление' })).toBeVisible()
  await page.getByRole('menuitem', { name: 'Сменить профиль' }).click()
  await expect(page).toHaveURL(/\/profiles$/)
  await expect(page.getByRole('heading', { name: 'Кто смотрит?' })).toBeVisible()

  // Protected profile: a wrong password is rejected, the right one gets in.
  await page.getByRole('button', { name: new RegExp(ADMIN.name) }).click()
  const pw = page.getByLabel('Пароль', { exact: true })
  await expect(pw).toBeFocused()
  await pw.fill('неверный')
  await pw.press('Enter')
  await expect(page.getByRole('alert')).toHaveText('Неверный пароль')
  await pw.fill(ADMIN.password)
  await page.getByRole('button', { name: 'Войти' }).click()

  // Real /api/home: hero with the test movie and «Недавно добавленные фильмы».
  await expect(page).toHaveURL(/\/$/)
  const hero = page.getByRole('region', { name: 'Рекомендуем' })
  await expect(hero.getByRole('heading', { name: LIBRARY_MOVIE.title })).toBeVisible()
  await expect(hero.getByRole('button', { name: 'Смотреть' })).toBeVisible()
  await expect(hero.getByRole('button', { name: 'Подробнее' })).toBeVisible()
  const backdrop = hero.locator('img').first()
  await expect(backdrop).toHaveJSProperty('complete', true)
  expect(await backdrop.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0)
  const recent = page.getByRole('region', { name: 'Недавно добавленные фильмы' })
  const card = recent.getByRole('link', { name: new RegExp(LIBRARY_MOVIE.title) })
  await expect(card).toBeVisible()
  expect(await card.locator('img').evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0)

  // The session survives a reload.
  await page.reload()
  await expect(page.getByRole('region', { name: 'Недавно добавленные фильмы' })).toBeVisible()

  // The pill highlights the current section.
  const nav = page.getByRole('navigation', { name: 'Разделы' })
  await nav.getByRole('link', { name: 'Фильмы', exact: true }).click()
  await expect(page).toHaveURL(/\/movies$/)
  await expect(nav.getByRole('link', { name: 'Фильмы', exact: true })).toHaveAttribute('aria-current', 'page')
  await expect(nav.getByRole('link', { name: 'Главная', exact: true })).not.toHaveAttribute('aria-current', 'page')
})

test('phone layout uses the bottom tab bar', async ({ page }) => {
  await setupAdmin(page.request)
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  const bar = page.getByTestId('tabbar')
  await expect(bar).toBeVisible()
  await expect(bar.getByRole('link', { name: 'Главная' })).toHaveAttribute('aria-current', 'page')
  await expect(page.getByTestId('topnav-pill')).toHaveCount(0)
})

test('keyboard: menu focus handling and a visible focus ring on cards', async ({ page }) => {
  await setupAdmin(page.request)
  await scanLibrary(page.request)
  await page.goto('/')
  const menuButton = page.getByRole('button', { name: `Профиль: ${ADMIN.name}` })
  await menuButton.focus()
  await page.keyboard.press('Enter')
  await expect(page.getByRole('menu')).toBeVisible()
  await expect(page.getByRole('menuitem').first()).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('menu')).toHaveCount(0)
  await expect(menuButton).toBeFocused()

  const card = page
    .getByRole('region', { name: 'Недавно добавленные фильмы' })
    .getByRole('link', { name: new RegExp(LIBRARY_MOVIE.title) })
  await card.focus()
  const outline = await card.locator('div').first().evaluate((el) => getComputedStyle(el).outlineStyle)
  expect(outline).toBe('solid')
})

test('an expired session goes back to «Кто смотрит?»', async ({ page }) => {
  await setupAdmin(page.request)
  await page.goto('/')
  await expect(page.getByRole('navigation', { name: 'Разделы' })).toBeVisible()
  // Revoke the session server-side, then make the app talk to the API again.
  await page.request.post('/api/logout')
  // «Фильмы» loads /api/items, which now answers 401.
  await page.getByRole('navigation', { name: 'Разделы' }).getByRole('link', { name: 'Фильмы', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Кто смотрит?' })).toBeVisible()
  await expect(page).toHaveURL(/\/profiles$/)
  await signInFromPicker(page)
})

test.describe('empty library', () => {
  test.use({ library: 'empty' })

  test('Главная shows a friendly empty state', async ({ page }) => {
    await setupAdmin(page.request)
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Медиатека пока пуста' })).toBeVisible()
    // the core build leads to the rescan, the other build to Search (emptyLibrary*.test.tsx)
    await expect(page.getByRole('link', { name: /Пересканировать в настройках|Найти фильм/ })).toBeVisible()
  })
})
