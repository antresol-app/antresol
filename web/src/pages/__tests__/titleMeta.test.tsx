// The metadata line of a title page: a part that is empty (a runtime that
// rounds to 0 minutes) leaves no separator behind («2022 · · Science Fiction»).
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { ItemDetail as Detail } from '../../api/types'
import { session } from '../../state/session'
import { ItemDetail } from '../ItemDetail'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })

const short = {
  id: 7,
  kind: 'movie',
  title: 'Короткий',
  subtitle: '',
  year: 2022,
  poster: '',
  backdrop: '',
  logo: '',
  progress: 0,
  onServer: true,
  overview: '',
  genres: ['Фантастика'],
  cast: [],
  files: [{ id: 70, name: 'Короткий.mkv', size: 1, durationMs: 6000, audio: [], subs: [] }],
  inWatchlist: false,
  seasons: [],
  actions: { play: true, toDevice: false, toServer: false, watchNow: false },
} as unknown as Detail

describe('title meta line', () => {
  beforeEach(() => session.reset())
  afterEach(() => vi.unstubAllGlobals())

  it('skips a runtime under half a minute without a double separator', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => (String(input).endsWith('/api/items/7') ? json(short) : json({}))),
    )
    render(
      <MemoryRouter initialEntries={['/item/7']}>
        <Routes>
          <Route path="/item/:id" element={<ItemDetail />} />
        </Routes>
      </MemoryRouter>,
    )
    await screen.findByRole('heading', { name: 'Короткий', level: 1 })
    const line = document.querySelector('p.meta') // the meta line (CSS modules are not scoped in tests)
    expect(line?.textContent).toBe('2022·Фантастика')
  })
})
