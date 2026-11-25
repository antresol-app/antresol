// Reference screenshots of «Состояние» (round 25.11): all in order and with
// something wrong in every group, in both languages, at 2560×1440 (the
// owner's display at 1x), a laptop window of 1280×610 and a phone of 390×844:
// the page, the whole page, and the sheets of a few rows. Opt-in, on
// new-headless Chrome (the visual project):
//   HEALTH_SHOTS=/some/folder npx playwright test e2e/screenshots-health.spec.ts --project visual
import path from 'node:path'
import { LANGS, mock, SIZES } from './health-world'
import { expect, setupAdmin, test } from './vault'

const OUT = process.env.HEALTH_SHOTS ?? ''
test.skip(!OUT, 'set HEALTH_SHOTS to a folder to take the screenshots')
test.use({ library: 'empty', colorScheme: 'dark', reducedMotion: 'reduce' })

for (const size of SIZES) {
  for (const lang of LANGS) {
    test.describe(`${lang.id} ${size.name}`, () => {
      test.use({ viewport: size.viewport, isMobile: size.phone, hasTouch: size.phone, locale: lang.locale })

      for (const broken of [false, true]) {
        test(`${broken ? 'with problems' : 'all in order'}`, async ({ page }) => {
          const shot = (name: string, fullPage = false) => page.screenshot({ path: path.join(OUT, `${name}-${lang.id}-${size.name}.png`), fullPage })
          await setupAdmin(page.request)
          await page.addInitScript((l) => localStorage.setItem('vault.uiLanguage', l), lang.id)
          await mock(page, broken)
          await page.goto('/manage/health')
          await expect(page.getByRole('heading', { name: lang.title, level: 2 })).toBeVisible()
          await expect(page.getByRole('img', { name: /^(Проверяем|Checking)/ })).toHaveCount(0)
          await page.evaluate(() => document.fonts.ready)
          const tag = broken ? 'broken' : 'ok'
          await shot(tag)
          await shot(`${tag}-full`, true)
          if (!broken) return
          for (const [name, button] of [
            ['sheet-connection', new RegExp(lang.broken)],
            ['sheet-folders', /(Папки медиатеки|Library Folders)/],
            ['sheet-tmdb', /TMDB/],
          ] as const) {
            await page.getByRole('button', { name: button }).click()
            await expect(page.getByRole('dialog')).toBeVisible()
            await page.waitForTimeout(400)
            await shot(name)
            await page.keyboard.press('Escape')
            await expect(page.getByRole('dialog')).toHaveCount(0)
          }
        })
      }
    })
  }
}
