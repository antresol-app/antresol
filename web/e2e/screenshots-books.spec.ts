// Reference screenshots of «Книги», a book page and the reader (three
// themes, settings, contents) and the PDF ribbon, against bin/vault with the
// generated books (books-media.ts). A few artwork-less filler books join the
// shelves and the grid so they show their real density. Opt-in:
//   SCREENSHOTS=1 npx playwright test e2e/screenshots-books.spec.ts --project chromium
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { Browser, Page } from '@playwright/test'
import { BOOKS } from './books-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

const HERE = path.dirname(fileURLToPath(import.meta.url))
const OUT = path.join(HERE, '..', '..', 'docs', 'screenshots')
const shot = (page: Page, name: string) => page.screenshot({ path: path.join(OUT, `books-${name}.jpg`), type: 'jpeg', quality: 85 })

test.skip(!process.env.SCREENSHOTS, 'set SCREENSHOTS=1 to refresh docs/screenshots')
test.use({ library: 'books' })
test.describe.configure({ timeout: 240_000 })

const VIEWPORTS = [
  { tag: 'desktop', width: 1440, height: 900, deviceScaleFactor: 2, isMobile: false },
  { tag: 'phone', width: 390, height: 844, deviceScaleFactor: 3, isMobile: true },
] as const

const FILLERS = [
  ['Мастер и Маргарита', 'Михаил Булгаков'],
  ['Пикник на обочине', 'Аркадий и Борис Стругацкие'],
  ['Воскресение', 'Лев Толстой'],
  ['Двенадцать стульев', 'Илья Ильф, Евгений Петров'],
  ['Белая гвардия', 'Михаил Булгаков'],
  ['Капитанская дочка', 'Александр Пушкин'],
  ['Обломов', 'Иван Гончаров'],
  ['Отцы и дети', 'Иван Тургенев'],
] as const

function fillers(start: number) {
  return FILLERS.map(([title, subtitle], i) => ({
    id: 900 + start + i,
    kind: 'book',
    title,
    subtitle,
    poster: '',
    backdrop: '',
    logo: '',
    progress: 0,
    onServer: true,
  }))
}

type Prefs = { fontScale: number; theme: string; font: string; columns: string; margin: string }

async function newPage(browser: Browser, baseURL: string, v: (typeof VIEWPORTS)[number], prefs?: Prefs) {
  const ctx = await browser.newContext({
    baseURL,
    viewport: { width: v.width, height: v.height },
    deviceScaleFactor: v.deviceScaleFactor,
    isMobile: v.isMobile,
    hasTouch: v.isMobile,
    colorScheme: 'dark',
    locale: 'ru-RU',
    reducedMotion: 'reduce',
  })
  if (prefs) await ctx.addInitScript((p) => localStorage.setItem('vault.reader.v1', JSON.stringify(p)), prefs)
  const page = await ctx.newPage()
  await page.route(/\/api\/books\/home$/, async (route) => {
    const res = await route.fetch()
    const json = (await res.json()) as { shelves: { id: string; cards: unknown[] }[] }
    for (const s of json.shelves) if (s.id === 'recent') s.cards = [...s.cards, ...fillers(0)]
    await route.fulfill({ response: res, json })
  })
  await page.route(/\/api\/books\?/, async (route) => {
    const res = await route.fetch()
    const json = (await res.json()) as { items: unknown[]; total: number }
    if (Number(new URL(route.request().url()).searchParams.get('offset') || 0) === 0) {
      json.items = [...json.items, ...fillers(20)]
      json.total = json.items.length
    }
    await route.fulfill({ response: res, json })
  })
  return page
}

const settle = async (page: Page, ms = 400) => {
  await page.waitForLoadState('networkidle')
  await page.waitForTimeout(ms)
}

async function readerReady(page: Page) {
  await expect(page.getByTestId('reader')).toHaveAttribute('data-ready', 'true', { timeout: 15_000 })
  await page.waitForTimeout(600)
}

test('books screenshots', async ({ browser, baseURL, page: first }) => {
  await setupAdmin(first.request)
  await scanLibrary(first.request, (s) => s.books >= 3)
  const cookies = await first.context().cookies()
  const list = (await (await first.request.get('/api/books?kind=book&sort=title')).json()) as { items: { id: number; title: string }[] }
  const id = (t: string) => list.items.find((b) => b.title === t)!.id
  const epub = id(BOOKS.epub)
  const fb2 = id(BOOKS.fb2)
  const pdf = id(BOOKS.pdf)
  // «Читаю сейчас»: the EPUB at 34 % (its locator is the start of chapter 1;
  // set again for each viewport in case a run moved it) and the FB2
  const reading = async () =>
    expect((await first.request.put(`/api/progress/${epub}`, { data: { locator: 'epubcfi(/6/2!/4/2)', fraction: 0.34 } })).ok()).toBeTruthy()
  await reading()
  expect((await first.request.put(`/api/progress/${fb2}`, { data: { locator: 'epubcfi(/6/4!/4/2)', fraction: 0.12 } })).ok()).toBeTruthy()
  // «Хочу прочитать»: the PDF
  expect((await first.request.post(`/api/watchlist/${pdf}`)).ok()).toBeTruthy()

  const themes: [string, Prefs][] = [
    ['original', { fontScale: 1, theme: 'original', font: 'system', columns: 'auto', margin: 'normal' }],
    ['paper', { fontScale: 1.1, theme: 'paper', font: 'iowan', columns: 'auto', margin: 'normal' }],
    ['night', { fontScale: 1, theme: 'night', font: 'georgia', columns: 'auto', margin: 'normal' }],
  ]

  for (const v of VIEWPORTS) {
    await reading()
    const page = await newPage(browser, baseURL!, v)
    await page.context().addCookies(cookies)

    await page.goto('/books')
    await expect(page.getByRole('heading', { name: 'Книги', level: 1 })).toBeVisible()
    await settle(page)
    if (!v.isMobile) await page.mouse.move(v.width - 10, 10)
    await shot(page, `home-${v.tag}`)

    await page.goto('/books/all/book')
    await expect(page.getByRole('heading', { name: 'Все книги', level: 1 })).toBeVisible()
    await settle(page)
    await shot(page, `grid-${v.tag}`)

    await page.goto(`/books/${epub}`)
    await expect(page.getByRole('heading', { name: BOOKS.epub, level: 1 })).toBeVisible()
    await settle(page)
    await shot(page, `detail-${v.tag}`)
    await page.close()

    // the reader in each theme, panels up as on open
    for (const [name, prefs] of themes) {
      const r = await newPage(browser, baseURL!, v, prefs)
      await r.context().addCookies(cookies)
      await r.goto(`/read/${epub}`)
      await readerReady(r)
      await shot(r, `reader-${name}-${v.tag}`)
      if (name === 'original') {
        await r.getByRole('button', { name: 'Оформление' }).click()
        await expect(r.getByRole('menu', { name: 'Оформление' })).toBeVisible()
        await r.waitForTimeout(400)
        await shot(r, `reader-settings-${v.tag}`)
        await r.keyboard.press('Escape')
        await r.getByRole('button', { name: 'Оглавление' }).click()
        await expect(r.getByRole('dialog', { name: 'Оглавление' })).toBeVisible()
        await r.waitForTimeout(400)
        await shot(r, `reader-toc-${v.tag}`)
      }
      await r.close()
    }

    const p = await newPage(browser, baseURL!, v)
    await p.context().addCookies(cookies)
    await p.goto(`/read/${pdf}`)
    await expect(p.locator('canvas[aria-label="Страница 1"]')).toHaveAttribute('data-drawn', 'true', { timeout: 15_000 })
    await p.waitForTimeout(400)
    await shot(p, `pdf-${v.tag}`)
    await p.close()
  }
})
