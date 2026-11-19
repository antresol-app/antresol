// «Файлы», «Список» with 5000 items: an arrow key redraws the two rows whose
// selection changed, not the whole folder (memoised rows, stable handlers).
// It counts renders, not milliseconds: a wall-clock bound measures the
// machine as much as the code (the old one, 50 ms a key, failed whenever the
// full suite ran beside another job, with 28 ms on an idle desktop), while
// the render count is exactly what the memoisation guarantees.
import { act, fireEvent, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { session } from '../../../state/session'
import { resetUsageCache } from '../StorageBar'
import { resetDirs } from '../useDir'
import { admin, entry, listing, mockApi, renderFiles } from './harness'

// every list row draws one Thumb, so its renders are the rows' renders
const drawn = vi.hoisted(() => ({ n: 0 }))
vi.mock('../Thumb', () => ({
  Thumb: () => {
    drawn.n++
    return null
  },
}))

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

describe('a folder with 5000 items', () => {
  it('an arrow key redraws the two rows whose selection changed (memoised rows, stable handlers)', async () => {
    const many = Array.from({ length: 5000 }, (_, i) => entry(`Файл ${String(i).padStart(4, '0')}.mkv`, { size: i, kind: 'video' }))
    await renderFiles(admin, mockApi(admin, { 'movies:': listing('movies', '', many) }), '/files/movies', /Файл 0000\.mkv/)
    fireEvent.click(screen.getByRole('radio', { name: 'Список' }))
    const grid = await screen.findByRole('treegrid')
    fireEvent.click(screen.getByRole('row', { name: 'Файл 0000.mkv' }))
    const press = () =>
      act(() => {
        fireEvent.keyDown(grid, { key: 'ArrowDown' })
      })
    press()
    drawn.n = 0
    for (let i = 0; i < 10; i++) press()
    expect(screen.getByRole('row', { name: 'Файл 0011.mkv' })).toHaveAttribute('aria-selected', 'true')
    // per key: the row that was selected and the one that is now (losing the
    // memo would be 5000 a key)
    expect(drawn.n).toBeLessThanOrEqual(10 * 2)
  }, 30_000)
})
