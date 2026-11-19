// «Аккаунты и синхронизация → Goodreads» against a real bin/vault with a fake shelf widget
// (fakes.ts): the profile is linked by its address, its shelves land on the
// shelves of «Книги», and a library export (CSV) adds the rest; the layout
// holds at a desktop and a phone width.
import { BOOKS } from './books-media'
import { FAKE_GOODREADS } from './fakes'
import { expect, scanLibrary, setupAdmin, test } from './vault'

test.use({ library: 'books', fakes: true })

// the service's own card: its heading's parent (the page's column holds every card)
const section = (page: import('@playwright/test').Page) => page.getByRole('heading', { name: 'Goodreads', exact: true }).locator('..')

const exportCSV =
  'Book Id,Title,Author,Author l-f,Additional Authors,ISBN,ISBN13,My Rating,Average Rating,Publisher,Binding,Number of Pages,Year Published,Original Publication Year,Date Read,Date Added,Bookshelves,Bookshelves with positions,Exclusive Shelf,My Review,Spoiler,Private Notes,Read Count,Owned Copies\n' +
  `1,${BOOKS.fb2},${BOOKS.fb2Author},,,"=""""","=""""",5,4,,,,,,2024/03/01,2024/01/01,,,read,,,,1,0\n` +
  `2,Совсем другая книга,Кто-то,,,"=""""","=""""",0,3,,,,,,,2024/01/01,,,to-read,,,,0,0\n`

test('a profile is linked, its shelves and an export land in «Книги»', async ({ page }) => {
  await setupAdmin(page.request)
  await scanLibrary(page.request, (s) => s.books >= 3)
  await page.goto('/settings/accounts')
  const gr = section(page)
  await expect(gr).toBeVisible()
  await gr.scrollIntoViewIfNeeded()
  const shots = process.env.GOODREADS_SHOTS
  if (shots) {
    // the state a profile meets first, at both widths
    await gr.screenshot({ path: `${shots}/unlinked-wide.png` })
    await page.setViewportSize({ width: 390, height: 844 })
    await gr.scrollIntoViewIfNeeded()
    await gr.screenshot({ path: `${shots}/unlinked-phone.png` })
    await page.setViewportSize({ width: 1440, height: 900 })
  }

  // an address with no profile number, an unknown profile: the server says so
  await gr.getByLabel('Адрес профиля на Goodreads').fill('https://www.goodreads.com/username')
  await gr.getByRole('button', { name: 'Связать' }).click()
  await expect(gr.getByText(/Не удалось найти номер профиля/)).toBeVisible()
  await gr.getByLabel('Адрес профиля на Goodreads').fill('https://www.goodreads.com/user/show/999-nobody')
  await gr.getByRole('button', { name: 'Связать' }).click()
  await expect(gr.getByText('Профиль Goodreads не найден или скрыт')).toBeVisible()

  await gr.getByLabel('Адрес профиля на Goodreads').fill(`https://www.goodreads.com/user/show/${FAKE_GOODREADS.profile}-reader`)
  await gr.getByRole('button', { name: 'Связать' }).click()
  await expect(gr.getByText('Связан как reader')).toBeVisible()
  await expect(gr.getByText(/Книг с Goodreads: 3 \(прочитано 2, читаю 0, хочу прочитать 1\), в библиотеке нашлось 2/)).toBeVisible({ timeout: 15_000 })
  await expect(gr.getByText(/Синхронизировано/)).toBeVisible()
  await expect(gr.getByText(/Только чтение/)).toBeVisible()

  // the export: whole history, no link needed
  await gr.getByTestId('goodreads-file').setInputFiles({ name: 'goodreads_library_export.csv', mimeType: 'text/csv', buffer: Buffer.from(exportCSV) })
  await expect(gr.getByText('Файл прочитан: 4 книги, в библиотеке нашлось 2.')).toBeVisible()

  // a file that is not an export
  await gr.getByTestId('goodreads-file').setInputFiles({ name: 'other.csv', mimeType: 'text/csv', buffer: Buffer.from('a,b\n1,2\n') })
  await expect(gr.getByText(/Это не экспорт библиотеки Goodreads/)).toBeVisible()

  if (shots) {
    await gr.screenshot({ path: `${shots}/settings-wide.png` })
    await page.setViewportSize({ width: 390, height: 844 })
    await gr.scrollIntoViewIfNeeded()
    await gr.screenshot({ path: `${shots}/settings-phone.png` })
    // nothing runs off the page sideways
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    await page.setViewportSize({ width: 1440, height: 900 })
  }

  // the books: read (the export's War and Peace, the profile's trial book), to-read
  const home = await (await page.request.get('/api/books/home')).json()
  const shelf = (id: string) => (home.shelves.find((s: { id: string }) => s.id === id)?.cards ?? []).map((c: { title: string }) => c.title)
  expect(shelf('done')).toEqual(expect.arrayContaining([BOOKS.epub, BOOKS.fb2]))
  await page.goto('/books')
  await expect(page.getByRole('heading', { name: 'Прочитано' })).toBeVisible()
  if (shots) await page.screenshot({ path: `${shots}/books.png` })

  // «Отвязать»: the link and the list go, the marks stay
  await page.goto('/settings/accounts')
  await gr.getByRole('button', { name: 'Отвязать' }).click()
  await expect(gr.getByLabel('Адрес профиля на Goodreads')).toBeVisible()
  await expect(gr.getByText(/Книг с Goodreads/)).toHaveCount(0)
  const after = await (await page.request.get('/api/books/home')).json()
  expect(after.shelves.find((s: { id: string }) => s.id === 'done')?.cards.length).toBeGreaterThanOrEqual(2)
})
