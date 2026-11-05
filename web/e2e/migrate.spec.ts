// «Управление → Переезд» is the admin's alone (the run and the switch to Vault:
// migrate.full.spec.ts).
import { expect, setupAdmin, test } from './vault'

test.use({ fakes: true, migrate: true })
test.describe.configure({ timeout: 90_000 })

test('not for other profiles', async ({ page }) => {
  await setupAdmin(page.request)
  const r = await page.request.post('/api/users', { data: { name: 'Маша', avatar: 'grad-3' } })
  expect(r.ok()).toBeTruthy()
  const { id } = (await r.json()) as { id: number }
  expect((await page.request.post('/api/login', { data: { userId: id } })).ok()).toBeTruthy()
  expect((await page.request.get('/api/migrate')).status()).toBe(403)
  await page.goto('/manage/migrate')
  await expect(page).toHaveURL(/\/$/)
})
