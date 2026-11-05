// Reference screenshots for docs/screenshots, with the curated fixture library
// (mocked /api/home). Opt-in:
//   SCREENSHOTS=1 npx playwright test e2e/screenshots.spec.ts --project chromium
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Browser, Page } from '@playwright/test'
import { mockLibrary } from './helpers'
import { ADMIN, expect, setupAdmin, test } from './vault'

const OUT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', '..', 'docs', 'screenshots')
const shot = (page: Page, name: string) => page.screenshot({ path: path.join(OUT, `${name}.jpg`), type: 'jpeg', quality: 85 })

test.skip(!process.env.SCREENSHOTS, 'set SCREENSHOTS=1 to refresh docs/screenshots')
test.use({ library: 'empty' })

const VIEWPORTS = [
  { tag: 'desktop', width: 1440, height: 900, deviceScaleFactor: 2, isMobile: false },
  { tag: 'phone', width: 390, height: 844, deviceScaleFactor: 3, isMobile: true },
] as const

async function newPage(browser: Browser, baseURL: string, v: (typeof VIEWPORTS)[number]) {
  const ctx = await browser.newContext({
    baseURL,
    viewport: { width: v.width, height: v.height },
    deviceScaleFactor: v.deviceScaleFactor,
    isMobile: v.isMobile,
    hasTouch: v.isMobile,
    colorScheme: 'dark',
    locale: 'ru-RU',
    reducedMotion: 'reduce', // stable frames
  })
  const page = await ctx.newPage()
  await mockLibrary(page)
  return page
}

test('screenshots', async ({ browser, vaultURL }) => {
  test.setTimeout(120_000)

  // First-run screen at both sizes, before the administrator exists.
  for (const v of VIEWPORTS) {
    const page = await newPage(browser, vaultURL, v)
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Добро пожаловать в Antresol' })).toBeVisible()
    await shot(page, `welcome-${v.tag}`)
    await page.context().close()
  }

  const admin = await newPage(browser, vaultURL, VIEWPORTS[0])
  await setupAdmin(admin.request)
  for (const u of [
    { name: 'Лена', avatar: 'grad-3' },
    { name: 'Маша', avatar: 'grad-4', isKid: true, maxRating: '6+' },
    { name: 'Гости', avatar: 'grad-7' },
  ]) {
    expect((await admin.request.post('/api/users', { data: u })).ok()).toBeTruthy()
  }
  await admin.context().close()

  for (const v of VIEWPORTS) {
    const page = await newPage(browser, vaultURL, v)
    await page.goto('/profiles')
    await expect(page.getByRole('heading', { name: 'Кто смотрит?' })).toBeVisible()
    await page.waitForTimeout(300)
    await shot(page, `profiles-${v.tag}`)
    await page.getByRole('button', { name: new RegExp(ADMIN.name) }).click()
    await expect(page.getByLabel('Пароль', { exact: true })).toBeFocused()
    await shot(page, `password-${v.tag}`)
    await page.getByLabel('Пароль', { exact: true }).fill(ADMIN.password)
    await page.getByRole('button', { name: 'Войти' }).click()

    await expect(page.getByRole('region', { name: 'Рекомендуем' })).toBeVisible()
    await page.waitForLoadState('networkidle')
    await page.waitForTimeout(600)
    await shot(page, `home-${v.tag}`)
    if (v.tag === 'desktop') {
      await page.getByRole('button', { name: /^2: / }).click()
      await page.mouse.move(700, 880)
      await page.waitForTimeout(500)
      await shot(page, 'home-hero-movie-desktop')
      await page.getByRole('button', { name: /^1: / }).click()
      const card = page.getByRole('region', { name: 'Продолжить' }).getByRole('link').nth(1)
      await card.focus()
      await page.keyboard.press('Shift+Tab')
      await page.keyboard.press('Tab')
      await page.waitForTimeout(400)
      await shot(page, 'home-focus-desktop')
      await page.mouse.move(700, 450)
    }
    await page.evaluate(() => window.scrollTo(0, 560))
    await page.waitForTimeout(500)
    await shot(page, `home-shelves-${v.tag}`)
    await page.evaluate(() => window.scrollTo(0, 0))
    await page.waitForTimeout(300)
    await page.getByRole('button', { name: `Профиль: ${ADMIN.name}` }).click()
    await expect(page.getByRole('menu')).toBeVisible()
    await page.waitForTimeout(300)
    await shot(page, `menu-${v.tag}`)
    await page.context().close()
  }
})
