// Kids' requests need no addon (docs/server-editions.md): against bin/vault of
// this tree, a kid profile finds a rated movie the library lacks in Search
// and asks for it; a parent answers in «Управление → Запросы» (nothing is
// downloaded: «Одобрить» only marks it); the kid's «Мои запросы» shows the
// answer. «Дети могут просить» at the top of that page turns requests off
// (the kid's «Найти и попросить» is gone) and on again. With
//   SCREENSHOTS=1 npx playwright test e2e/kid-requests.spec.ts --project chromium
// the parent's page is saved at three sizes (SHOTS_DIR: where to).
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Page } from '@playwright/test'
import { FAKE_KIDS_MOVIE } from './fakes'
import { expect, setupAdmin, test } from './vault'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const OUT = process.env.SHOTS_DIR ?? path.join(HERE, '..', '..', 'docs', 'screenshots')
const SHOOT = Boolean(process.env.SCREENSHOTS)

test.use({ library: 'sample', fakes: true })

const SIZES = [
  { name: '2560x1440', width: 2560, height: 1440 },
  { name: '1280x610', width: 1280, height: 610 },
  { name: '390x844', width: 390, height: 844 },
]

async function shoot(page: Page, name: string) {
  if (!SHOOT) return
  const keep = page.viewportSize()!
  for (const s of SIZES) {
    await page.setViewportSize({ width: s.width, height: s.height })
    await page.waitForTimeout(400)
    await page.screenshot({ path: path.join(OUT, `kid-requests-${name}-${s.name}.png`), type: 'png', scale: 'css' })
  }
  await page.setViewportSize(keep)
}

test('a kid asks, a parent answers, the parent turns requests off and on', async ({ browser, page, vaultURL }) => {
  await setupAdmin(page.request)
  const made = await page.request.post('/api/users', { data: { name: 'Вася', avatar: 'grad-2', isKid: true, maxRating: '12+' } })
  expect(made.status(), await made.text()).toBe(201)
  const kidId = ((await made.json()) as { id: number }).id
  const kidCtx = await browser.newContext({ baseURL: vaultURL, locale: 'ru-RU', colorScheme: 'dark', serviceWorkers: 'block' })
  try {
    expect((await kidCtx.request.post('/api/login', { data: { userId: kidId } })).ok()).toBeTruthy()
    const kid = await kidCtx.newPage()
    const askLabel = `Попросить «${FAKE_KIDS_MOVIE.title}»`

    // the kid: the movie the library lacks is a result to ask for
    await kid.goto('/search')
    await kid.getByRole('searchbox').fill('мульт')
    await expect(kid.getByRole('heading', { name: 'Найти и попросить' })).toBeVisible()
    await kid.getByRole('button', { name: askLabel }).click()
    await expect(kid.getByText('Ждёт ответа')).toBeVisible()

    // the parent: the request waits, the switch is on; «Одобрить» marks it
    await page.goto('/manage/requests')
    const toggle = page.getByRole('switch', { name: 'Дети могут просить' })
    await expect(toggle).toHaveAttribute('aria-checked', 'true')
    await expect(page.getByRole('button', { name: `Одобрить «${FAKE_KIDS_MOVIE.title}»` })).toBeVisible()
    await shoot(page, 'on')
    await page.getByRole('button', { name: `Одобрить «${FAKE_KIDS_MOVIE.title}»` }).click()
    await expect(page.getByRole('list', { name: 'Отвеченные за 30 дней' }).getByText('Одобрено', { exact: true })).toBeVisible()

    // the kid sees the answer: nothing is in the library yet
    await kid.goto('/requests')
    await expect(kid.getByText('Одобрено — скоро появится')).toBeVisible()

    // the parent turns requests off: the kid's search offers nothing to ask for
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-checked', 'false')
    await page.reload()
    await expect(page.getByRole('switch', { name: 'Дети могут просить' })).toHaveAttribute('aria-checked', 'false')
    await expect(page.getByRole('list', { name: 'Отвеченные за 30 дней' })).toBeVisible() // the list stays
    await shoot(page, 'off')
    await kid.goto('/search')
    await kid.getByRole('searchbox').fill('мульт')
    await expect(kid.getByText('Ничего не найдено в медиатеке')).toBeVisible()
    await expect(kid.getByRole('heading', { name: 'Найти и попросить' })).toHaveCount(0)

    // and on again
    await page.getByRole('switch', { name: 'Дети могут просить' }).click()
    await expect(page.getByRole('switch', { name: 'Дети могут просить' })).toHaveAttribute('aria-checked', 'true')
    await kid.goto('/search')
    await kid.getByRole('searchbox').fill('мульт')
    await expect(kid.getByRole('heading', { name: 'Найти и попросить' })).toBeVisible()
  } finally {
    await kidCtx.close()
  }
})
