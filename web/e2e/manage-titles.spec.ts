// A group title of the settings pages keeps its 32 px above it whatever order
// its page's stylesheets load in. It composes ui/List's title (margin: 0 0 8px)
// and sets its own margin-top; a production build loads the chunk with that
// copy after Manage's, and with two rules of the same weight the later one won:
// the titles sat flush against what is above them. Only the built bundle has
// that order, so this runs against bin/vault's embedded web.
import { expect, scanLibrary, setupAdmin, test } from './vault'

test.use({ library: 'sample' })

test('a group title that follows something has 32 px above it', async ({ page }) => {
  await setupAdmin(page.request)
  await scanLibrary(page.request)
  await page.goto('/manage/files')
  const title = page.getByRole('heading', { level: 3, name: 'Больше всего места' })
  await expect(title).toBeVisible()
  await expect(title).toHaveCSS('margin-top', '32px')
})
