// One layout for «Настройки» and «Управление» on a wide window: every page
// that sits beside the sidebar starts at the content's left edge (the profile's
// own pages used to be a column centred in what was left of it), and the
// profiles' «Изменить» buttons stand in one column whichever profile has more
// buttons (a kid's has two extra).
import { expect, setupAdmin, test } from './vault'

test.use({ library: 'empty' })

test('every page beside the sidebar starts at the same left edge', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 610 })
  await setupAdmin(page.request)
  const edges: Record<string, number> = {}
  for (const path of ['/settings', '/settings/accounts', '/settings/api-keys', '/manage/profiles', '/manage/settings/watching', '/manage/import']) {
    await page.goto(path)
    const head = page.locator('#main section header').first()
    await head.waitFor()
    edges[path] = Math.round((await head.boundingBox())!.x)
  }
  expect(new Set(Object.values(edges)).size, JSON.stringify(edges)).toBe(1)
})

test('profiles: «Изменить» is in one column on every row', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 610 })
  await setupAdmin(page.request)
  for (const data of [{ name: 'Лена', avatar: 'grad-2' }, { name: 'Маша', avatar: 'grad-3', isKid: true, maxRating: '6+' }]) {
    expect((await page.request.post('/api/users', { data })).ok()).toBeTruthy()
  }
  await page.goto('/manage/profiles')
  const buttons = page.getByRole('button', { name: 'Изменить', exact: true })
  await expect(buttons).toHaveCount(3)
  const lefts = new Set<number>()
  const rights = new Set<number>()
  for (const b of await buttons.all()) {
    const box = (await b.boundingBox())!
    lefts.add(Math.round(box.x))
    rights.add(Math.round(box.x + box.width))
  }
  expect([...lefts], 'left edges of «Изменить»').toHaveLength(1)
  expect([...rights], 'right edges of «Изменить»').toHaveLength(1)
  // and the trash buttons after it too
  const trash = page.getByRole('button', { name: /^Удалить профиль/ })
  const xs = new Set<number>()
  for (const b of await trash.all()) xs.add(Math.round((await b.boundingBox())!.x))
  expect([...xs], 'trash buttons').toHaveLength(1)
})

// A tablet's window: the slot kept for a kid's two buttons is not kept (it would squeeze an adult's text and cut «Изменить»).
test('profiles on a 800 pt window: no button label is cut', async ({ page }) => {
  await page.setViewportSize({ width: 800, height: 700 })
  await setupAdmin(page.request)
  for (const data of [{ name: 'Лена', avatar: 'grad-2' }, { name: 'Маша', avatar: 'grad-3', isKid: true, maxRating: '6+' }]) {
    expect((await page.request.post('/api/users', { data })).ok()).toBeTruthy()
  }
  await page.goto('/manage/profiles')
  const buttons = page.getByRole('button', { name: 'Изменить', exact: true })
  await expect(buttons).toHaveCount(3)
  for (const b of await buttons.all()) {
    expect(await b.evaluate((el) => [...el.querySelectorAll('*')].every((c) => c.scrollWidth <= c.clientWidth + 1))).toBe(true)
  }
  // the administrator's role is not broken inside a word
  const role = page.getByText('Администратор', { exact: true })
  expect(((await role.boundingBox())!.height)).toBeLessThan(24)
})
