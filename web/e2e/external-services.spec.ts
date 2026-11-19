// «Внешние сервисы» on a real server: the first-run wizard's fourth step
// lists every service checked and saves the choice with «Готово»; the
// settings screen shows it and a switch applies at once; a server that
// never chose shows «Состояние»'s one-time notice. EXT_SHOTS (a folder)
// keeps screenshots of each screen at the viewport's width.
import path from 'node:path'
import type { Page } from '@playwright/test'
import { ADMIN, expect, test } from './vault'

// the services unset in the environment (the e2e default turns three off)
test.use({ env: { VAULT_WIKIDATA_BASEURL: '', VAULT_IMDB_RATINGS_URL: '', VAULT_SEGMENTS_ONLINE: '', VAULT_LRCLIB_URL: '' } })

const shot = async (page: Page, name: string) => {
  const dir = process.env.EXT_SHOTS
  if (!dir) return
  const w = page.viewportSize()?.width ?? 0
  await page.screenshot({ path: path.join(dir, `${name}-${w}.png`), fullPage: true })
}

async function createAdmin(page: Page) {
  await page.goto('/')
  await page.waitForURL(/\/welcome$/)
  await page.getByLabel('Ваше имя').fill(ADMIN.name)
  await page.getByLabel('Пароль', { exact: true }).fill(ADMIN.password)
  await page.getByRole('button', { name: 'Продолжить' }).click()
  await page.waitForURL(/\/setup\/wizard$/)
}

const sw = (page: Page, name: string) => page.getByRole('switch', { name })

for (const width of [390, 1440]) {
  test.describe(`at ${width}px`, () => {
    test.use({ viewport: { width, height: width < 600 ? 844 : 900 } })

    test('the wizard offers every service checked and saves the choice; the settings apply it at once', async ({ page }) => {
      await createAdmin(page)
      for (let i = 0; i < 3; i++) await page.getByRole('button', { name: 'Пропустить', exact: true }).click()
      await expect(page.getByRole('heading', { name: 'Внешние сервисы' })).toBeVisible()
      await expect(page.getByText('Шаг 4 из 4')).toBeVisible()
      for (const name of ['Wikidata', 'Рейтинги IMDb', 'Тексты песен (LRCLIB)', 'Заставки и титры (TheIntroDB, IntroDB)']) {
        await expect(sw(page, name)).toHaveAttribute('aria-checked', 'true')
      }
      await shot(page, 'wizard-external')
      await sw(page, 'Тексты песен (LRCLIB)').click()
      await page.getByRole('button', { name: 'Готово' }).click()
      await expect(page.getByRole('navigation', { name: 'Разделы' })).toBeVisible()

      await page.goto('/manage/settings/external')
      await expect(page.getByRole('heading', { name: 'Внешние сервисы' })).toBeVisible()
      await expect(sw(page, 'Тексты песен (LRCLIB)')).toHaveAttribute('aria-checked', 'false')
      await expect(sw(page, 'Рейтинги IMDb')).toHaveAttribute('aria-checked', 'true')
      await expect(page.getByRole('link', { name: 'wikidata.org' })).toBeVisible()
      await shot(page, 'settings-external')
      // a switch saves at once and the server's answer holds after a reload
      const saved = page.waitForResponse((r) => r.url().endsWith('/api/settings/external') && r.request().method() === 'PUT')
      await sw(page, 'Wikidata').click()
      expect((await saved).ok()).toBe(true)
      await page.reload()
      await expect(sw(page, 'Wikidata')).toHaveAttribute('aria-checked', 'false')
      // chosen: «Состояние» has no notice
      await page.goto('/manage/health')
      await expect(page.getByRole('heading', { name: 'Состояние' })).toBeVisible()
      await expect(page.getByText('Выберите внешние сервисы')).toHaveCount(0)
      // nothing wider than the screen
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    })

    test('a server that never chose shows the one-time notice on «Состояние»', async ({ page }) => {
      await createAdmin(page)
      await page.getByRole('button', { name: 'Пропустить настройку' }).click()
      await expect(page.getByRole('navigation', { name: 'Разделы' })).toBeVisible()
      await page.goto('/manage/health')
      await expect(page.getByText('Выберите внешние сервисы')).toBeVisible()
      await shot(page, 'health-notice')
      await page.getByRole('link', { name: 'Открыть' }).click()
      await expect(page).toHaveURL(/\/manage\/settings\/external$/)
      // IMDb and lyrics wait for a yes; Wikidata and the intro databases are on
      await expect(sw(page, 'Рейтинги IMDb')).toHaveAttribute('aria-checked', 'false')
      await expect(sw(page, 'Wikidata')).toHaveAttribute('aria-checked', 'true')
      await page.goto('/manage/health')
      await expect(page.getByRole('heading', { name: 'Состояние' })).toBeVisible()
      await expect(page.getByText('Выберите внешние сервисы')).toHaveCount(0)
    })
  })
}
