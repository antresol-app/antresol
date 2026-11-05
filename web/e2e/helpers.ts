import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect, type Page } from '@playwright/test'
import { homeFixture } from './fixtures/home'

const IMAGES = path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures', 'images')

/**
 * Replaces /api/home and /api/images/* with the curated fixture library (real
 * artwork from fixtures/images). Used only by the screenshot spec, so the
 * reference pictures always show a full, stable Главная.
 */
export async function mockLibrary(page: Page, home = homeFixture) {
  // the app asks /api/home?online=1 (online entries of «Продолжить просмотр»)
  await page.route(/\/api\/home(\?|$)/, (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(home) }),
  )
  await page.route('**/api/images/*', async (route) => {
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').pop() ?? '')
    try {
      const body = await readFile(path.join(IMAGES, path.basename(name)))
      await route.fulfill({ status: 200, contentType: name.endsWith('.png') ? 'image/png' : 'image/jpeg', body })
    } catch {
      await route.fulfill({ status: 404, body: '' })
    }
  })
}

import { ADMIN } from './vault'
export { ADMIN }

/** Gets the page to Главная whatever state the server is in. */
export async function signIn(page: Page) {
  await page.goto('/')
  await page.waitForURL(/\/(welcome|profiles)?$/)
  if (page.url().endsWith('/welcome')) {
    await page.getByLabel('Ваше имя').fill(ADMIN.name)
    await page.getByLabel('Пароль', { exact: true }).fill(ADMIN.password)
    await page.getByRole('button', { name: 'Продолжить' }).click()
  } else if (page.url().endsWith('/profiles')) {
    await page.getByRole('button', { name: new RegExp(ADMIN.name) }).click()
    await page.getByLabel('Пароль', { exact: true }).fill(ADMIN.password)
    await page.getByRole('button', { name: 'Войти' }).click()
  }
  await expect(page.getByRole('navigation', { name: 'Разделы' })).toBeVisible()
}
