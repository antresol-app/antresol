// Files an addon holds: a badge with its words in the list, a row in
// «Свойства» with the addon's name.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { session } from '../../../state/session'
import { resetUsageCache } from '../StorageBar'
import { resetDirs } from '../useDir'
import { admin, entry, listing, mockApi, renderFiles } from './harness'

const LISTINGS = {
  'movies:': listing('movies', '', [
    entry('Pack', { isDir: true, kind: 'folder', childCount: 2, hold: { addon: 'Держатель', label: 'Используется', active: true } }),
    entry('Old.mkv', { kind: 'video', hold: { addon: 'Держатель', label: '', active: false } }),
    entry('Free.mkv', { kind: 'video' }),
  ]),
}

beforeEach(() => {
  session.reset()
  resetDirs()
  resetUsageCache()
  try {
    localStorage.clear()
  } catch {
    /* ignore */
  }
})
afterEach(() => vi.unstubAllGlobals())

describe('held files', () => {
  it('the list shows the hold badge, «Свойства» the addon', async () => {
    const u = userEvent.setup()
    await renderFiles(admin, mockApi(admin, LISTINGS), '/files/movies', /Pack/)
    await u.click(screen.getByRole('radio', { name: 'Список' }))
    const row = (name: string) => screen.getAllByRole('row').find((r) => r.getAttribute('data-name') === name)!
    expect(within(row('Pack')).getByText('Используется')).toHaveAttribute('title', 'Эти данные использует расширение «Держатель»')
    expect(within(row('Old.mkv')).getByText('Держатель')).toBeInTheDocument()
    expect(within(row('Free.mkv')).queryByTitle(/расширение/)).not.toBeInTheDocument()

    await u.click(row('Pack'))
    await u.keyboard('{Meta>}i{/Meta}')
    const info = await screen.findByRole('complementary', { name: 'Свойства' })
    expect(await within(info).findByText('Держатель · Используется')).toBeInTheDocument()
    expect(within(info).getByText('Расширение')).toBeInTheDocument()
  })
})
