// Reference screenshots of «Файлы» against bin/vault with the generated media
// root (files-media.ts). Opt-in:
//   SCREENSHOTS=1 npx playwright test e2e/screenshots-files.spec.ts --project chromium
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Browser, Page } from '@playwright/test'
import { FILES } from './files-media'
import { expect, setupAdmin, test } from './vault'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const OUT = path.join(HERE, '..', '..', 'docs', 'screenshots')
const shot = (page: Page, name: string) => page.screenshot({ path: path.join(OUT, `files-${name}.jpg`), type: 'jpeg', quality: 85 })

test.skip(!process.env.SCREENSHOTS, 'set SCREENSHOTS=1 to refresh docs/screenshots')
test.use({ library: 'files' })
test.describe.configure({ timeout: 180_000 })

async function newPage(browser: Browser, baseURL: string, phone: boolean) {
  const ctx = await browser.newContext({
    baseURL,
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: phone ? 3 : 2,
    isMobile: phone,
    hasTouch: phone,
    locale: 'ru-RU',
    colorScheme: 'dark',
  })
  return ctx.newPage()
}

async function ready(page: Page) {
  await setupAdmin(page.request)
  await expect
    .poll(async () => ((await (await page.request.get('/api/items?kind=movie')).json()) as { total: number }).total, { timeout: 20_000 })
    .toBeGreaterThanOrEqual(2)
}

/** Waits until every thumbnail on screen has loaded or given up. */
async function settle(page: Page) {
  await page.waitForTimeout(400)
  await expect
    .poll(
      () =>
        page.evaluate(() =>
          [...document.querySelectorAll('img')].every((i) => i.complete),
        ),
      { timeout: 15_000 },
    )
    .toBe(true)
  await page.waitForTimeout(400)
}

const folderUrl = `/files/movies/${encodeURIComponent(FILES.movie.folder)}`

test('desktop', async ({ browser, baseURL }) => {
  const page = await newPage(browser, baseURL!, false)
  await ready(page)
  // A trashed item for the «Корзина» picture.
  await page.request.post('/api/files/trash', { data: { root: 'downloads', paths: ['Seven.Worlds.One.Planet.S01.1080p.BluRay/Seven.Worlds.One.Planet.nfo'] } })

  await page.goto('/files/movies')
  await page.getByRole('option', { name: FILES.movie.folder, exact: true }).click()
  await settle(page)
  await shot(page, 'icons-desktop')

  await page.goto(folderUrl)
  await page.getByRole('option', { name: FILES.movie.video, exact: true }).click()
  await settle(page)
  await shot(page, 'icons-folder-desktop')

  // Свойства of the video.
  await page.keyboard.press('ControlOrMeta+i')
  await expect(page.getByRole('complementary', { name: 'Свойства' }).getByText('Длительность')).toBeVisible({ timeout: 15_000 })
  await settle(page)
  await shot(page, 'info-desktop')
  await page.keyboard.press('ControlOrMeta+i')

  // List with a folder open in place.
  await page.goto('/files/series')
  await page.getByRole('radio', { name: 'Список' }).click()
  const show = page.getByRole('row', { name: FILES.series.folder, exact: true })
  await show.click()
  await page.keyboard.press('ArrowRight')
  await page.getByRole('row', { name: 'Season 01', exact: true }).click()
  await page.keyboard.press('ArrowRight')
  await page.getByRole('row', { name: 'Планета Земля II.S01E02.mp4', exact: true }).click()
  await settle(page)
  await shot(page, 'list-desktop')

  // Columns with a file preview.
  await page.goto(folderUrl)
  await page.getByRole('radio', { name: 'Колонки' }).click()
  await page.getByRole('option', { name: 'poster.jpg', exact: true }).click()
  await settle(page)
  await shot(page, 'columns-desktop')

  // Context menu.
  await page.getByRole('option', { name: FILES.movie.video, exact: true }).click({ button: 'right' })
  await page.waitForTimeout(300)
  await shot(page, 'menu-desktop')
  await page.keyboard.press('Escape')

  // Quick Look of the poster and of the subtitles.
  await page.getByRole('radio', { name: 'Значки' }).click()
  await page.getByRole('option', { name: 'poster.jpg', exact: true }).click()
  await page.keyboard.press('Space')
  await settle(page)
  await shot(page, 'quicklook-desktop')
  await page.keyboard.press('Escape')
  await page.getByRole('option', { name: FILES.movie.srt, exact: true }).click()
  await page.keyboard.press('Space')
  await expect(page.getByText('Привет из субтитров')).toBeVisible()
  await shot(page, 'quicklook-text-desktop')
  await page.keyboard.press('Escape')

  // Confirmation sheet.
  await page.getByRole('option', { name: FILES.movie.srt, exact: true }).click()
  await page.keyboard.press('ControlOrMeta+Backspace')
  await page.waitForTimeout(300)
  await shot(page, 'trash-confirm-desktop')
  await page.keyboard.press('Escape')

  await page.goto('/files/trash')
  await page.getByRole('row', { name: 'Seven.Worlds.One.Planet.nfo', exact: true }).click()
  await page.waitForTimeout(300)
  await shot(page, 'trash-desktop')

  // Storage legend.
  await page.getByRole('button', { name: 'Хранилище: подробнее' }).click()
  await page.waitForTimeout(300)
  await shot(page, 'storage-desktop')
})

test('phone', async ({ browser, baseURL }) => {
  const page = await newPage(browser, baseURL!, true)
  await ready(page)
  await page.goto('/files')
  await settle(page)
  await shot(page, 'overview-phone')

  await page.goto('/files/movies')
  await settle(page)
  await shot(page, 'icons-phone')

  await page.goto(folderUrl)
  await page.getByRole('button', { name: 'Вид и сортировка' }).click()
  await page.getByRole('menuitemradio', { name: 'Список' }).click()
  await settle(page)
  await shot(page, 'list-phone')

  await page.getByRole('button', { name: `Действия: ${FILES.movie.video}` }).click()
  await page.waitForTimeout(400)
  await shot(page, 'actions-phone')
  await page.getByRole('menuitem', { name: 'Свойства' }).click()
  await expect(page.getByText('Длительность')).toBeVisible({ timeout: 15_000 })
  await settle(page)
  await shot(page, 'info-phone')
  await page.getByRole('button', { name: 'Закрыть' }).click()

  await page.getByRole('row', { name: 'poster.jpg', exact: true }).click()
  await settle(page)
  await shot(page, 'quicklook-phone')
})
