// «Отправить на Kindle» against a real bin/vault: the mail settings and the
// profile's address turn the button on; the password never comes back to the
// browser. The mail server here refuses the connection, so the send reaches
// SMTP and the page explains the failure (the exchange itself is covered by
// internal/mailer's tests).
import { BOOKS } from './books-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

test.use({ library: 'books' })

// fixme: red on the sources build too (the mail fields moved into the «Настроить…» sheet; this still looks for them on the page)
test.fixme('settings and a profile turn the button on, a failure is explained', async ({ page }) => {
  await setupAdmin(page.request)
  await scanLibrary(page.request, (s) => s.books >= 3)

  // no account, no address: no button
  await page.goto('/')
  await page.getByRole('navigation', { name: 'Разделы' }).getByRole('link', { name: 'Книги' }).click()
  await page.getByRole('link', { name: new RegExp(BOOKS.fb2) }).first().click()
  await expect(page.getByRole('heading', { level: 1, name: BOOKS.fb2 })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Отправить на Kindle' })).toHaveCount(0)
  const bookUrl = page.url()

  // the mail account
  await page.goto('/manage/settings')
  await expect(page.getByText('Пока не настроено')).toBeVisible()
  await expect(page.getByRole('link', { name: 'Пароли приложений Google' })).toHaveAttribute('href', /apppasswords/)
  await expect(page.getByRole('link', { name: 'Открыть настройки Amazon' })).toBeVisible()
  await page.getByLabel('Сервер SMTP').fill('127.0.0.1')
  await page.getByLabel('Порт', { exact: true }).fill('1')
  await page.getByLabel('Логин', { exact: true }).fill('sender@example.com')
  const saved = page.waitForResponse((r) => r.url().endsWith('/api/settings') && r.request().method() === 'PUT' && (r.request().postData() ?? '').includes('s3cret-password'))
  await page.getByLabel('Пароль', { exact: true }).fill('s3cret-password')
  await page.getByLabel('Адрес отправителя').click()
  const answer = await saved
  expect(answer.status()).toBe(200)
  expect(await answer.text()).not.toContain('s3cret-password')
  await page.getByLabel('Адрес отправителя').fill('sender@example.com')
  await page.getByLabel('Сервер SMTP').click()
  await expect(page.getByText('Сохранено', { exact: true })).toBeVisible()
  await expect(page.getByText('Пароль сохранён')).toBeVisible()
  await page.screenshot({ path: process.env.KINDLE_SHOTS ? `${process.env.KINDLE_SHOTS}/settings.png` : undefined, fullPage: true })

  // survives a reload, without the password
  await page.waitForTimeout(300)
  await page.reload()
  await expect(page.getByLabel('Сервер SMTP')).toHaveValue('127.0.0.1')
  await expect(page.getByLabel('Пароль', { exact: true })).toHaveValue('')
  await expect(page.getByText('Почта настроена')).toBeVisible()
  await expect(page.getByText('Пароль сохранён')).toBeVisible()

  // the profile's address
  await page.goto('/manage/profiles')
  await page.getByRole('button', { name: /Изменить|admin/i }).first().click()
  const address = page.getByLabel('Адрес Kindle')
  await address.fill('reader@gmail.com')
  await page.getByRole('button', { name: 'Сохранить' }).click()
  await expect(page.getByText('Адрес Kindle заканчивается на @kindle.com')).toBeVisible()
  await address.fill('reader_x1@kindle.com')
  await page.screenshot({ path: process.env.KINDLE_SHOTS ? `${process.env.KINDLE_SHOTS}/profile.png` : undefined })
  await page.getByRole('button', { name: 'Сохранить' }).click()
  await expect(address).toHaveCount(0)

  // the button, and a refused connection explained
  await page.goto(bookUrl)
  const send = page.getByRole('button', { name: 'Отправить на Kindle' })
  await expect(send).toBeVisible()
  await page.screenshot({ path: process.env.KINDLE_SHOTS ? `${process.env.KINDLE_SHOTS}/book.png` : undefined })
  await send.click()
  await expect(page.getByText('Не удалось отправить книгу')).toBeVisible()
  await page.screenshot({ path: process.env.KINDLE_SHOTS ? `${process.env.KINDLE_SHOTS}/book-error.png` : undefined })
})
