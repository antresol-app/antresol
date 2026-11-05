// «Книги» and the reader against a real bin/vault with the generated books
// (books-media.ts): the EPUB reopens where it was left, the Windows-1251 FB2
// shows Cyrillic, the PDF draws its first page and «Ночь» turns the page black.
import type { Frame, Page } from '@playwright/test'
import { BOOKS, evilEpub, TRACKER } from './books-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

test.use({ library: 'books' })

async function openBook(page: Page, title: string) {
  await setupAdmin(page.request)
  await scanLibrary(page.request, (s) => s.books >= 3)
  await page.goto('/')
  await page.getByRole('navigation', { name: 'Разделы' }).getByRole('link', { name: 'Книги' }).click()
  await expect(page).toHaveURL(/\/books$/)
  await expect(page.getByRole('heading', { level: 1, name: 'Книги' })).toBeVisible()
  await page.getByRole('link', { name: new RegExp(title) }).first().click()
  await expect(page).toHaveURL(/\/books\/\d+$/)
  await expect(page.getByRole('heading', { level: 1, name: title })).toBeVisible()
}

/** The foliate section frame whose body is laid out (inside closed shadow roots). */
async function bookFrame(page: Page): Promise<Frame> {
  let found: Frame | undefined
  await expect
    .poll(async () => {
      for (const f of page.frames()) {
        if (f === page.mainFrame()) continue
        const ok = await f.evaluate(() => (document.body?.innerText ?? '').trim().length > 0).catch(() => false)
        if (ok) {
          found = f
          return true
        }
      }
      return false
    }, { timeout: 15_000 })
    .toBe(true)
  return found!
}

const locator = (page: Page) => page.getByTestId('reader').getAttribute('data-locator')

/** Waits until the EPUB reader has opened the book and taken its position. */
async function ready(page: Page) {
  await bookFrame(page)
  await expect(page.getByTestId('reader')).toHaveAttribute('data-ready', 'true', { timeout: 15_000 })
  await expect.poll(() => locator(page)).toMatch(/^epubcfi\(/)
}

test('epub reopens at the same place', async ({ page }) => {
  await openBook(page, BOOKS.epub)
  await expect(page.getByText(`${BOOKS.epubSeries} · книга 2`)).toBeVisible()
  await page.getByRole('link', { name: 'Читать' }).click()
  await expect(page).toHaveURL(/\/read\/\d+$/)
  await ready(page)
  const first = await locator(page)
  for (let i = 0; i < 3; i++) {
    const before = await locator(page)
    await page.keyboard.press('ArrowRight')
    await expect.poll(() => locator(page)).not.toBe(before)
    await page.waitForTimeout(400) // foliate ignores keys while a page turns
  }
  const cfi = await locator(page)
  expect(cfi).not.toBe(first)

  // «‹ Книги» saves before leaving
  // turning pages hid the panels; a click in the middle brings them back
  await expect(page.getByTestId('reader').locator('[data-hidden]')).toHaveCount(1)
  await page.mouse.click(720, 450)
  await page.getByRole('button', { name: 'Книги' }).click()
  await expect(page).toHaveURL(/\/books\/\d+$/)
  const resume = page.getByRole('link', { name: /^Продолжить · \d+\s?%$/ })
  await expect(resume).toBeVisible()
  const id = Number(new URL(page.url()).pathname.split('/').pop())
  const saved = (await (await page.request.get(`/api/books/${id}`)).json()) as { progress: { locator: string; fraction: number } }
  expect(saved.progress.locator).toBe(cfi)
  expect(saved.progress.fraction).toBeGreaterThan(0)

  await resume.click()
  await expect(page).toHaveURL(/\/read\/\d+$/)
  await ready(page)
  await expect.poll(() => locator(page)).toBe(cfi)
})

test('fb2 in windows-1251 shows Cyrillic', async ({ page }) => {
  await openBook(page, BOOKS.fb2)
  await page.getByRole('link', { name: 'Читать' }).click()
  await ready(page)
  // the title section, then the first chapter — decoded from Windows-1251
  const frame = await bookFrame(page)
  await expect(frame.locator('body')).toContainText(BOOKS.fb2)
  await page.keyboard.press('ArrowRight')
  await expect
    .poll(async () => {
      const texts = await Promise.all(page.frames().map((f) => f.evaluate(() => document.body?.innerText ?? '').catch(() => '')))
      return texts.some((t) => t.includes('Анна Павловна Шерер') && !t.includes('\uFFFD'))
    })
    .toBe(true)
})

test('pdf renders first page canvas', async ({ page }) => {
  await openBook(page, BOOKS.pdf)
  await page.getByRole('link', { name: 'Читать' }).click()
  const canvas = page.locator('canvas[aria-label="Страница 1"]')
  await expect(canvas).toHaveAttribute('data-drawn', 'true', { timeout: 15_000 })
  const size = await canvas.evaluate((c: HTMLCanvasElement) => ({ w: c.width, h: c.height }))
  expect(size.w).toBeGreaterThan(100)
  expect(size.h).toBeGreaterThan(size.w)
  await expect(page.getByText('Страница 1 из 1')).toBeVisible()
})

test('theme Ночь applies black background', async ({ page }) => {
  await openBook(page, BOOKS.epub)
  await page.getByRole('link', { name: 'Читать' }).click()
  await ready(page)
  const frame = await bookFrame(page)
  await page.getByRole('button', { name: 'Оформление' }).click()
  await page.getByRole('menuitemradio', { name: 'Ночь' }).click()
  await expect
    .poll(() => frame.evaluate(() => getComputedStyle(document.documentElement).backgroundColor))
    .toBe('rgb(0, 0, 0)')
  await expect(page.getByTestId('reader')).toHaveCSS('background-color', 'rgb(0, 0, 0)')
  // remembered on this device
  await page.reload()
  await ready(page)
  const again = await bookFrame(page)
  await expect.poll(() => again.evaluate(() => getComputedStyle(document.documentElement).backgroundColor)).toBe('rgb(0, 0, 0)')
})

test('search finds a phrase and the contents jump to a chapter', async ({ page }) => {
  await openBook(page, BOOKS.epub)
  await page.getByRole('link', { name: 'Читать' }).click()
  await ready(page)
  await page.getByRole('button', { name: 'Поиск' }).click()
  const sheet = page.getByRole('dialog', { name: 'Поиск по книге' })
  await sheet.getByRole('searchbox').fill('колокольня')
  await sheet.getByRole('button', { name: 'Найти' }).click()
  const hits = sheet.getByRole('list', { name: 'Результаты поиска' })
  await expect(hits.getByRole('button').first()).toBeVisible({ timeout: 15_000 })
  await expect(sheet.getByText(/^Найдено \d+ совпадени/)).toBeVisible()
  await hits.getByRole('button').last().click()
  await expect(sheet).toHaveCount(0)
  await expect.poll(() => locator(page)).toMatch(/^epubcfi\(\/6\/6!/) // the last chapter

  await page.getByRole('button', { name: 'Оглавление' }).click()
  const toc = page.getByRole('dialog', { name: 'Оглавление' })
  await expect(toc.getByRole('button', { name: BOOKS.chapters[2] })).toHaveAttribute('aria-current', 'true')
  await toc.getByRole('button', { name: BOOKS.chapters[1] }).click()
  await expect.poll(() => locator(page)).toMatch(/^epubcfi\(\/6\/4!/)
})

/** Opens the evil EPUB; `stripCsp` serves the app without its CSP header (the book sanitizer alone). */
async function openEvil(page: Page, stripCsp: boolean) {
  const evil = evilEpub()
  const seen = { api: [] as string[], tracker: 0 }
  page.on('request', (r) => {
    const u = new URL(r.url())
    // the app itself calls /api/me; nothing from a book frame may call /api at all
    const fromBook = (() => {
      try {
        return r.frame() !== page.mainFrame()
      } catch {
        return true // a service-worker or detached-frame request
      }
    })()
    if (u.pathname.startsWith('/api/') && (fromBook || u.pathname.startsWith('/api/me/app-password'))) seen.api.push(`${r.method()} ${u.pathname}`)
  })
  await page.route(`${new URL(TRACKER).origin}/**`, (r) => {
    seen.tracker++
    return r.fulfill({ status: 200, contentType: 'image/png', body: Buffer.alloc(0) })
  })
  if (stripCsp)
    await page.route(
      (u) => u.protocol.startsWith('http') && !u.pathname.startsWith('/api') && !u.pathname.startsWith('/assets'),
      async (r) => {
        const resp = await r.fetch()
        const headers = { ...resp.headers() }
        delete headers['content-security-policy']
        await r.fulfill({ response: resp, headers })
      },
    )
  await openBook(page, BOOKS.epub)
  await page.route('**/api/stream/**', (r) => r.fulfill({ status: 200, contentType: 'application/epub+zip', body: evil }))
  await page.getByRole('link', { name: 'Читать' }).click()
  await ready(page)
  const texts = async () => Promise.all(page.frames().map((f) => f.evaluate(() => document.body?.innerText ?? '').catch(() => '')))
  // both sections render (the non-exact type too), sanitized
  await expect.poll(async () => (await texts()).some((t) => t.includes('Typed evil section'))).toBe(true)
  await page.keyboard.press('ArrowRight')
  await page.waitForTimeout(400)
  await page.keyboard.press('ArrowRight')
  await expect.poll(async () => (await texts()).some((t) => t.includes('Hello evil book'))).toBe(true)
  await page.waitForTimeout(1500)
  const flags = await page.evaluate(() => {
    const w = window as unknown as Record<string, unknown>
    return ['__inline', '__external', '__onerror', '__api', '__typed', '__typedError', '__srcdoc'].filter((k) => w[k] !== undefined)
  })
  expect(flags).toEqual([])
  expect(seen.api).toEqual([])
  expect(seen.tracker).toBe(0)
}

test('scripts in a book do not run', async ({ page }) => {
  await openEvil(page, false)
  // and the app document carries the policy that stops them
  const res = await page.request.get('/books')
  expect(res.headers()['content-security-policy']).toContain("script-src 'self'")
})

test('the book sanitizer alone stops scripts and remote images', async ({ page }) => {
  await openEvil(page, true)
})

test('fb2 in windows-1251 without a declaration shows Cyrillic', async ({ page }) => {
  await openBook(page, BOOKS.fb2Undeclared)
  await page.getByRole('link', { name: 'Читать' }).click()
  await ready(page)
  await expect
    .poll(async () => {
      const texts = await Promise.all(page.frames().map((f) => f.evaluate(() => document.body?.innerText ?? '').catch(() => '')))
      return texts.some((t) => t.includes('Все счастливые семьи похожи друг на друга') && !t.includes('\uFFFD'))
    })
    .toBe(true)
})

test('opening a book does not save a position', async ({ page }) => {
  await openBook(page, BOOKS.pdf)
  const id = Number(new URL(page.url()).pathname.split('/').pop())
  await page.getByRole('link', { name: 'Читать' }).click()
  await expect(page.locator('canvas[aria-label="Страница 1"]')).toHaveAttribute('data-drawn', 'true', { timeout: 15_000 })
  await page.waitForTimeout(2600) // past the 2 s debounce
  // the panels hide after 3 s (ReaderChrome HIDE_AFTER_MS); focus brings them back
  const back = page.getByRole('button', { name: 'Книги' })
  await back.focus()
  await back.click()
  await expect(page).toHaveURL(/\/books\/\d+$/)
  await expect(page.getByRole('link', { name: 'Читать' })).toBeVisible()
  const detail = (await (await page.request.get(`/api/books/${id}`)).json()) as { progress: unknown }
  expect(detail.progress).toBeNull()
})

test('the keyboard alone: a key shows the panels, focus keeps them, Esc shows them and then leaves', async ({ page, browserName }) => {
  // Safari moves focus to buttons with ⌥Tab (Tab alone: fields and links)
  const tab = browserName === 'webkit' ? 'Alt+Tab' : 'Tab'
  await openBook(page, BOOKS.epub)
  await page.getByRole('link', { name: 'Читать' }).click()
  await ready(page)
  const hidden = page.getByTestId('reader').locator('[data-hidden]')
  const back = page.getByRole('button', { name: 'Книги' })
  await page.keyboard.press('ArrowRight')
  await expect(hidden).toHaveCount(1)

  // Esc first only brings the panels back
  await page.keyboard.press('Escape')
  await expect(hidden).toHaveCount(0)
  await expect(page).toHaveURL(/\/read\/\d+$/)
  await expect(hidden).toHaveCount(1, { timeout: 6000 })

  // Tab reaches «‹ Книги»; the panels stay while focus is in them
  for (let i = 0; i < 6 && !(await back.evaluate((b) => b === document.activeElement)); i++) {
    await page.keyboard.press(tab)
    await expect(hidden).toHaveCount(0)
  }
  await expect(back).toBeFocused()
  await page.waitForTimeout(4000)
  await expect(hidden).toHaveCount(0)

  // with the panels up, Esc leaves the book
  await page.keyboard.press('Escape')
  await expect(page).toHaveURL(/\/books\/\d+$/)
})

for (const [kind, title] of [
  ['epub', BOOKS.epub],
  ['pdf', BOOKS.pdf],
] as const) {
  test(`a ${kind} that cannot be opened keeps a way back`, async ({ page }) => {
    await openBook(page, title)
    await page.route('**/api/stream/*', (route) => route.fulfill({ status: 200, contentType: 'application/octet-stream', body: 'это не книга' }))
    await page.getByRole('link', { name: 'Читать' }).click()
    const alert = page.getByRole('alert').filter({ hasText: /Не удалось открыть/ })
    await expect(alert).toBeVisible({ timeout: 15_000 })
    await page.waitForTimeout(3500) // past the panels' auto-hide
    await expect(page.getByTestId('reader').locator('[data-hidden]')).toHaveCount(0)
    await alert.getByRole('button', { name: 'Назад' }).click()
    await expect(page).toHaveURL(/\/books\/\d+$/)
  })
}
