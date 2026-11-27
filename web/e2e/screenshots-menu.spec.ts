// The profile menu, the app's Menu as an iOS 26 menu, for comparing with the
// native one (a simulator capture): open, a row under the pointer, the
// library group's checked row; desktop at 1x and a phone; an admin, a long
// name, a kid. Opt-in, written to MENU_SHOTS (a folder):
//   MENU_SHOTS=/tmp/menu npx playwright test e2e/screenshots-menu.spec.ts --project visual
import path from 'node:path'
import type { Browser, Page } from '@playwright/test'
import { mockLibrary } from './helpers'
import { ADMIN, expect, setupAdmin, test } from './vault'

const OUT = process.env.MENU_SHOTS ?? ''
test.skip(!OUT, 'set MENU_SHOTS=<folder> to take the menu screenshots')
test.use({ library: 'empty' })

const VIEWPORTS = [
  { tag: 'desktop', width: 1440, height: 900, deviceScaleFactor: 1, isMobile: false },
  { tag: 'phone', width: 390, height: 844, deviceScaleFactor: 3, isMobile: true },
] as const

// the longest a name gets: 64 bytes (internal/auth), 30 Cyrillic letters
const LONG = 'Александра Константинопольская'

async function signedIn(browser: Browser, baseURL: string, v: (typeof VIEWPORTS)[number], userId: number) {
  const ctx = await browser.newContext({
    baseURL,
    viewport: { width: v.width, height: v.height },
    deviceScaleFactor: v.deviceScaleFactor,
    isMobile: v.isMobile,
    hasTouch: v.isMobile,
    colorScheme: 'dark',
    locale: 'ru-RU',
  })
  expect((await ctx.request.post('/api/login', { data: { userId, password: userId === 1 ? ADMIN.password : undefined } })).ok()).toBeTruthy()
  const page = await ctx.newPage()
  await mockLibrary(page)
  return page
}

async function open(page: Page, name: string) {
  await page.goto('/')
  await page.getByRole('button', { name: `Профиль: ${name}` }).click()
  await expect(page.getByRole('menu')).toBeVisible()
  await page.waitForTimeout(600) // past the grow animation
}

const shot = (page: Page, name: string) => page.screenshot({ path: path.join(OUT, `${name}.png`) })

test('menu screenshots', async ({ browser, page, vaultURL }) => {
  test.setTimeout(180_000)
  await setupAdmin(page.request)
  const long = await page.request.post('/api/users', { data: { name: LONG, avatar: 'grad-3' } })
  expect(long.status(), await long.text()).toBe(201)
  const longId = ((await long.json()) as { id: number }).id
  const kid = await page.request.post('/api/users', { data: { name: 'Вася', avatar: 'grad-2', isKid: true, maxRating: '12+' } })
  const kidId = ((await kid.json()) as { id: number }).id

  for (const v of VIEWPORTS) {
    const admin = await signedIn(browser, vaultURL, v, 1)
    await open(admin, ADMIN.name)
    await shot(admin, `admin-open-${v.tag}`)
    // the closing animation, mid-way, and the opening one
    const settings = admin.getByRole('menuitem', { name: 'Настройки' })
    if (!v.isMobile) {
      await settings.hover()
      await admin.waitForTimeout(200)
      await shot(admin, `admin-hover-${v.tag}`)
    } else {
      // a finger held on the row: the capsule (:active)
      const b = (await settings.boundingBox())!
      await admin.mouse.move(b.x + b.width / 2, b.y + b.height / 2)
      await admin.mouse.down()
      await admin.waitForTimeout(150)
      await shot(admin, `admin-press-${v.tag}`)
      await admin.mouse.up()
      await open(admin, ADMIN.name)
    }
    const mine = admin.getByRole('menuitemradio', { name: 'Моя медиатека' })
    if (await mine.count()) {
      await mine.click()
      await expect(mine).toHaveAttribute('aria-checked', 'true')
      await admin.mouse.move(5, 5)
      await admin.waitForTimeout(300)
      await shot(admin, `admin-library-mine-${v.tag}`)
    }
    await admin.keyboard.press('Escape')
    await admin.goto('/')
    const trigger = admin.getByRole('button', { name: `Профиль: ${ADMIN.name}` })
    await trigger.click()
    await admin.waitForTimeout(90)
    await shot(admin, `admin-growing-${v.tag}`)
    await admin.context().close()

    const l = await signedIn(browser, vaultURL, v, longId)
    await open(l, LONG)
    await shot(l, `long-open-${v.tag}`)
    await l.context().close()

    const k = await signedIn(browser, vaultURL, v, kidId)
    await open(k, 'Вася')
    await shot(k, `kid-open-${v.tag}`)
    await k.context().close()
  }
})
