// A separate history's own app password, end to end: made in «Устройства →
// Отдельные истории», then a Jellyfin client signs in with the profile's
// name and it (the TV's Infuse) and is the profile, and a Subsonic client
// signs in the same way; the profile's own app password is untouched.
import { ADMIN, setupAdmin, expect, test } from './vault'

test.use({ library: 'empty' })

const shots = process.env.E2E_SHOTS_DIR
// E2E_SHOTS_DIR: screenshots for a person to judge the layout, after the
// sheets' animations settle
async function shot(page: import('@playwright/test').Page, o: { path: string }) {
  await page.waitForTimeout(700)
  await page.screenshot(o)
}

test('a separate history gets an app password that Jellyfin and Subsonic sign in with', async ({ page }) => {
  await setupAdmin(page.request)
  const created = await page.request.post('/api/me/managed', { data: { name: 'Гостиная' } })
  expect(created.status()).toBe(201)

  await page.goto('/settings')
  await page.getByRole('button', { name: 'Устройства' }).click()
  const devices = page.getByRole('dialog', { name: 'Устройства' })
  await devices.getByRole('button', { name: 'Действия: Гостиная' }).click()
  await page.getByRole('menuitem', { name: 'Пароль для приложений' }).click()
  const sheet = page.getByRole('dialog', { name: 'Пароль для приложений: Гостиная' })
  await expect(sheet.getByText(/запишется в «Гостиная»/)).toBeVisible()
  await sheet.getByRole('button', { name: 'Создать пароль' }).click()
  const field = sheet.getByRole('textbox', { name: 'Пароль' })
  await expect(field).toHaveValue(/^[a-z0-9]{4}(-[a-z0-9]{4}){3}$/)
  const password = await field.inputValue()
  if (shots) await shot(page, { path: `${shots}/managed-password-${page.viewportSize()?.width}.png` })

  // Jellyfin: the profile signs in with the account's password
  const jf = await page.request.post('/Users/AuthenticateByName', {
    headers: { Authorization: 'MediaBrowser DeviceId="ATV-e2e", Device="Apple TV", Client="Infuse-Direct", Version="8.5.6"' },
    data: { Username: ADMIN.name, Pw: password },
  })
  expect(jf.status(), await jf.text()).toBe(200)
  const auth = (await jf.json()) as { AccessToken: string; User: { Name: string } }
  expect(auth.User.Name).toBe(ADMIN.name)
  expect(auth.AccessToken).toMatch(/^[0-9a-f]{32}$/)

  // Subsonic: the same name and password
  const ping = await page.request.get(
    `/rest/ping?u=${encodeURIComponent(ADMIN.name)}&p=${encodeURIComponent(password)}&c=e2e&v=1.16.1&f=json`,
  )
  expect(((await ping.json()) as { 'subsonic-response': { status: string } })['subsonic-response'].status).toBe('ok')

  // the profile's own app password was not made by this
  const own = (await (await page.request.get('/api/me/app-password')).json()) as { exists: boolean }
  expect(own.exists).toBe(false)
  await sheet.getByRole('button', { name: 'Готово' }).click()
  if (shots) await shot(page, { path: `${shots}/managed-row-${page.viewportSize()?.width}.png` })
})

test('the separate histories fit a phone', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await setupAdmin(page.request)
  expect((await page.request.post('/api/me/managed', { data: { name: 'Гостиная' } })).status()).toBe(201)
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Устройства' }).click()
  const devices = page.getByRole('dialog', { name: 'Устройства' })
  const button = devices.getByRole('button', { name: 'Действия: Гостиная' })
  await button.scrollIntoViewIfNeeded()
  await expect(button).toBeInViewport()
  // nothing of the row runs off the sheet
  const box = (await devices.boundingBox())!
  for (const b of await devices.getByRole('listitem').filter({ hasText: 'Гостиная' }).getByRole('button').all()) {
    const bb = (await b.boundingBox())!
    expect(bb.x + bb.width).toBeLessThanOrEqual(box.x + box.width + 1)
  }
  if (shots) await shot(page, { path: `${shots}/managed-row-390.png` })
  await button.click()
  await expect(page.getByRole('menuitem', { name: 'Пароль для приложений' })).toBeVisible()
  if (shots) await shot(page, { path: `${shots}/managed-menu-390.png` })
  await page.getByRole('menuitem', { name: 'Пароль для приложений' }).click()
  await page.getByRole('dialog', { name: 'Пароль для приложений: Гостиная' }).getByRole('button', { name: 'Создать пароль' }).click()
  if (shots) await shot(page, { path: `${shots}/managed-password-390.png` })
})
