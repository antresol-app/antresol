// «Состояние» as the System Settings list of checks (round 25.11): a ring and
// a short value on each row, a sheet «Как починить» on a tap. The server's
// answers are mocked (health-world.ts) so every state can be on the page at
// once, with the longest strings the page can meet (a long addon name, a long
// reason), in both languages and at the sizes the owner uses: 2560×1440 (1x),
// a laptop window of 1280×610 and a phone of 390×844. In each of them no
// row's name and value overlap or leave the row, the page does not scroll
// sideways, and the sheet stays in the window. The screenshots are
// screenshots-health.spec.ts.
import { layoutProblems, LANGS, mock, SIZES } from './health-world'
import { expect, setupAdmin, test } from './vault'

// no slide-in: the sheet's box is measured where it rests
test.use({ library: 'empty', reducedMotion: 'reduce' })

for (const size of SIZES) {
  for (const lang of LANGS) {
    test.describe(`${lang.id} ${size.name}`, () => {
      test.use({ viewport: size.viewport, isMobile: size.phone, hasTouch: size.phone, locale: lang.locale })

      for (const broken of [false, true]) {
        test(`${broken ? 'with problems' : 'all in order'}`, async ({ page }) => {
          await setupAdmin(page.request)
          await page.addInitScript((l) => localStorage.setItem('vault.uiLanguage', l), lang.id)
          await mock(page, broken)
          await page.goto('/manage/health')
          await expect(page.getByRole('heading', { name: lang.title, level: 2 })).toBeVisible()
          // every row has its answer: nothing is still «Проверяем»
          await expect(page.getByRole('img', { name: /^(Проверяем|Checking)/ })).toHaveCount(0)
          if (broken) await expect(page.getByText(/^(Сломано|Broken): \d/)).toBeVisible()
          else await expect(page.getByText(lang.id === 'ru' ? 'Всё работает' : 'Everything works')).toBeVisible()
          expect(await layoutProblems(page), 'the list').toEqual([])

          if (!broken) {
            // nothing to open on a row that is fine
            await expect(page.getByRole('button', { name: new RegExp(lang.broken) })).toHaveCount(0)
            return
          }
          // the first broken row: its sheet says what is wrong and how to fix it, and fits the window
          await page.getByRole('button', { name: new RegExp(lang.broken) }).click()
          const sheet = page.getByRole('dialog')
          await expect(sheet.getByText(lang.sheetFix)).toBeVisible()
          await expect(sheet.getByText('VAULT_TRUSTED_PROXIES=172.18.0.1')).toBeVisible()
          await expect(sheet.getByRole('button', { name: lang.copy })).toBeVisible()
          const box = await sheet.boundingBox()
          expect(box, 'the sheet is on screen').not.toBeNull()
          expect(box!.x).toBeGreaterThanOrEqual(0)
          expect(box!.x + box!.width).toBeLessThanOrEqual(size.viewport.width)
          expect(box!.y + box!.height).toBeLessThanOrEqual(size.viewport.height)
          expect(await sheet.locator('[data-sheet-body]').evaluate((e) => e.scrollWidth <= e.clientWidth + 1)).toBe(true)
          await page.keyboard.press('Escape')
          await expect(sheet).toHaveCount(0)

          // the others of the owner's sheets: a command of the folders, TMDB's button
          await page.getByRole('button', { name: /(Папки медиатеки|Library Folders)/ }).click()
          await expect(page.getByRole('dialog').getByText('sudo chown -R 1000:1000 /srv/media/movies')).toBeVisible()
          await page.keyboard.press('Escape')
          await page.getByRole('button', { name: /TMDB/ }).click()
          await expect(page.getByRole('dialog').getByRole('button', { name: lang.id === 'ru' ? 'Проверить снова' : 'Check Again' })).toBeVisible()
        })
      }
    })
  }
}
