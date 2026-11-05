// The button's states in a real browser, the send itself answered here: the
// real exchange is covered by internal/mailer's tests and kindle.spec.ts.
import { BOOKS } from './books-media'
import { expect, scanLibrary, setupAdmin, test } from './vault'

test.use({ library: 'books' })

test('Sent, then On Kindle, then a question before sending again', async ({ page }) => {
  await setupAdmin(page.request)
  await scanLibrary(page.request, (s) => s.books >= 3)
  await page.route(/\/api\/books\/\d+$/, async (route) => {
    const res = await route.fetch()
    await route.fulfill({ response: res, json: { ...(await res.json()), canSendToKindle: true } })
  })
  let posts = 0
  await page.route(/\/api\/books\/\d+\/kindle$/, (route) => {
    posts++
    return route.fulfill({ status: 204 })
  })
  await page.goto('/books')
  await page.getByRole('link', { name: new RegExp(BOOKS.fb2) }).first().click()
  await page.getByRole('button', { name: 'Отправить на Kindle' }).click()

  const sent = page.getByRole('button', { name: 'Отправлено' })
  await expect(sent).toBeVisible()
  await page.screenshot({ path: process.env.KINDLE_SHOTS ? `${process.env.KINDLE_SHOTS}/btn-sent.png` : undefined })
  expect(await sent.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe('rgb(255, 255, 255)')

  const onKindle = page.getByRole('button', { name: 'На Kindle' })
  await expect(onKindle).toBeVisible({ timeout: 4000 })
  await expect(page.getByText('Эта книга уже отправлена')).toHaveCount(0)

  await onKindle.click()
  await expect(page.getByText('Эта книга уже отправлена. Отправить ещё раз?')).toBeVisible()
  await page.waitForTimeout(400) // the menu's fade
  await page.screenshot({ path: process.env.KINDLE_SHOTS ? `${process.env.KINDLE_SHOTS}/btn-menu.png` : undefined })
  expect(posts).toBe(1)
  await page.getByRole('menuitem', { name: 'Отправить ещё раз' }).click()
  await expect(page.getByRole('button', { name: 'Отправлено' })).toBeVisible()
  expect(posts).toBe(2)

  await page.setViewportSize({ width: 390, height: 780 })
  await page.waitForTimeout(2800)
  await page.screenshot({ path: process.env.KINDLE_SHOTS ? `${process.env.KINDLE_SHOTS}/btn-phone.png` : undefined })
})
