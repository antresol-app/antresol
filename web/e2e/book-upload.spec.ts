// «Добавить книги» against a real bin/vault: a file from the device reaches the
// library at once through the picker and through a drop, a non-book is refused,
// and the book is in «Моя медиатека» of whoever added it.
import { BOOKS } from './books-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

test.use({ library: 'books' })

const fb2 = (title: string) =>
  `<?xml version="1.0" encoding="UTF-8"?><FictionBook><description><title-info><book-title>${title}</book-title><lang>ru</lang></title-info></description><body><section><p>Текст</p></section></body></FictionBook>`

test('a book from the device appears in the library', async ({ page }) => {
  await setupAdmin(page.request)
  await scanLibrary(page.request, (s) => s.books >= 3)
  await page.goto('/books')
  await expect(page.getByRole('heading', { level: 1, name: 'Книги' })).toBeVisible()
  await expect(page.getByRole('link', { name: new RegExp(BOOKS.fb2) }).first()).toBeVisible()

  // the picker
  const input = page.getByTestId('book-upload-input')
  await input.setInputFiles({ name: 'Личная книга.fb2', mimeType: 'application/octet-stream', buffer: Buffer.from(fb2('Личная книга')) })
  await expect(page.getByText('Добавлена 1 книга.')).toBeVisible()
  await expect(page.getByRole('link', { name: /Личная книга/ }).first()).toBeVisible()

  // a drop anywhere on the page
  const data = await page.evaluateHandle((text) => {
    const dt = new DataTransfer()
    dt.items.add(new File([text], 'Брошенная.fb2'))
    return dt
  }, fb2('Брошенная книга'))
  await page.dispatchEvent('body', 'dragenter', { dataTransfer: data })
  await expect(page.getByText('Отпустите, чтобы добавить книги')).toBeVisible()
  await page.dispatchEvent('body', 'drop', { dataTransfer: data })
  await expect(page.getByText('Отпустите, чтобы добавить книги')).toHaveCount(0)
  await expect(page.getByRole('link', { name: /Брошенная книга/ }).first()).toBeVisible()

  // a refusal names the file; the others still go
  await input.setInputFiles([
    { name: 'фото.jpg', mimeType: 'image/jpeg', buffer: Buffer.from('jpeg') },
    { name: 'Третья.fb2', mimeType: 'application/octet-stream', buffer: Buffer.from(fb2('Третья книга')) },
  ])
  await expect(page.getByText(/фото\.jpg: Это не книга/)).toBeVisible()
  await expect(page.getByRole('link', { name: /Третья книга/ }).first()).toBeVisible()

  // the uploader's own library has them, not the books of the scan
  const mine = await (await page.request.get('/api/books?kind=book&scope=mine')).text()
  expect(mine).toContain('Личная книга')
  expect(mine).not.toContain(BOOKS.fb2)
})
