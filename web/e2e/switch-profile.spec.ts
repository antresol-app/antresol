// «Сменить профиль» keeps the session until another profile signs in; the
// session cookie is a long rolling one (it outlives a restarted browser and
// days of a clock); the password step is a form password managers save.
import { setupAdmin, expect, test, ADMIN } from './vault'

test.use({ library: 'empty' })

const session = async (page: import('@playwright/test').Page) =>
  (await page.context().cookies()).find((c) => c.name === 'vault_session')

test('switch profile, then back: still signed in; signing in as another replaces the session', async ({ page }) => {
  await setupAdmin(page.request)
  const create = await page.request.post('/api/users', { data: { name: 'Маша', password: 'маша-1234' } })
  expect(create.status()).toBe(201)
  await page.goto('/settings')
  const before = await session(page)
  expect(before).toBeTruthy()

  await page.getByRole('button', { name: new RegExp(ADMIN.name) }).first().click()
  await page.getByRole('menuitem', { name: 'Сменить профиль' }).click()
  await expect(page).toHaveURL(/\/profiles$/)
  await expect(page.getByRole('heading', { name: 'Кто смотрит?' })).toBeVisible()
  // a password step entered and left keeps the session as well
  await page.getByRole('button', { name: /Маша/ }).click()
  await page.getByRole('button', { name: 'Назад' }).click()
  await page.getByRole('button', { name: 'Отмена' }).click()
  await expect(page).toHaveURL(/\/settings$/)
  expect((await page.request.get('/api/me')).status()).toBe(200)

  // Escape and the browser's Back return too
  await page.getByRole('button', { name: new RegExp(ADMIN.name) }).first().click()
  await page.getByRole('menuitem', { name: 'Сменить профиль' }).click()
  await expect(page.getByRole('heading', { name: 'Кто смотрит?' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page).toHaveURL(/\/settings$/)
  await page.reload()
  await expect(page.getByRole('navigation', { name: 'Разделы' })).toBeVisible()
  expect((await session(page))?.value).toBe(before!.value)

  // another profile actually signing in is what ends the old session
  await page.getByRole('button', { name: new RegExp(ADMIN.name) }).first().click()
  await page.getByRole('menuitem', { name: 'Сменить профиль' }).click()
  await page.getByRole('button', { name: /Маша/ }).click()
  // what Chrome's and Safari's password managers look for
  const form = page.locator('form', { has: page.locator('input[autocomplete="current-password"]') })
  await expect(form.locator('input[autocomplete="username"]')).toHaveValue('Маша')
  await expect(form.locator('button[type="submit"]')).toBeVisible()
  await form.locator('input[autocomplete="current-password"]').fill('маша-1234')
  await form.locator('button[type="submit"]').click()
  await expect(page).toHaveURL(/\/settings$/)
  const after = await session(page)
  expect(after?.value).not.toBe(before!.value)
  const me = await (await page.request.get('/api/me')).json()
  expect(me.name).toBe('Маша')
  const old = await page.request.get('/api/me', { headers: { Cookie: `vault_session=${before!.value}` } })
  expect(old.status()).toBe(401)
})

test('the session survives days and a browser restart; each start renews the cookie', async ({ page, browser, vaultURL }) => {
  await setupAdmin(page.request)
  await page.goto('/')
  await expect(page.getByRole('navigation', { name: 'Разделы' })).toBeVisible()
  const c = (await session(page))!
  // a persistent cookie (not session-only), about 400 days long
  const days = (c.expires - Date.now() / 1000) / 86400
  expect(days).toBeGreaterThan(399)
  expect(c.httpOnly).toBe(true)

  // a new browser (only the stored cookie carried over), its clock 3 days on
  const later = await browser.newContext({ baseURL: vaultURL, locale: 'ru-RU' })
  await later.addCookies([c])
  const p = await later.newPage()
  await p.clock.install({ time: Date.now() + 3 * 86400_000 })
  const renewed = p.waitForResponse((r) => r.url().endsWith('/api/me'))
  await p.goto('/')
  await expect(p.getByRole('navigation', { name: 'Разделы' })).toBeVisible()
  expect((await (await renewed).headerValue('set-cookie')) ?? '').toMatch(/vault_session=.*Max-Age=34560000/)
  await later.close()
})
