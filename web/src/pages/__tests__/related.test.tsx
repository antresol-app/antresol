// «Коллекция «…»» and «Похожие» under the cast of a title page, and the
// collection page: library cards open the item, a TMDB title the library
// lacks opens its title page (sources build), a watched part says so, and an
// empty row is not drawn.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router'
import type { Card, CollectionRow, ItemDetail as Detail, RelatedRows } from '../../api/types'
import { session } from '../../state/session'
import { Collection } from '../Collection'
import { ItemDetail } from '../ItemDetail'

const json = (b: unknown, status = 200) =>
  new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const movie = {
  id: 5,
  kind: 'movie',
  title: 'Матрица',
  subtitle: '',
  year: 1999,
  poster: '',
  backdrop: '',
  logo: '',
  progress: 0,
  onServer: true,
  overview: 'Нео',
  genres: [],
  cast: [],
  files: [],
  inWatchlist: false,
  seasons: [],
  actions: { play: false, toDevice: false, toServer: false, watchNow: false },
  tmdbId: 603,
} as unknown as Detail

const lib = (id: number, title: string, year: number, extra: Partial<Card> = {}): Card => ({
  id,
  kind: 'movie',
  title,
  subtitle: '',
  year,
  poster: '',
  backdrop: '',
  logo: '',
  progress: 0,
  onServer: true,
  ...extra,
})
const missing = (tmdbId: number, title: string, year: number): Card => ({
  ...lib(0, title, year),
  onServer: false,
  tmdbKind: 'movie',
  tmdbId,
})

const collection: CollectionRow = {
  id: 2344,
  name: 'Матрица',
  overview: 'Четыре фильма о Нео',
  backdrop: 'https://image.test/w1280/cb.jpg',
  parts: [lib(5, 'Матрица', 1999), lib(6, 'Матрица: Перезагрузка', 2003, { watched: true }), missing(605, 'Матрица: Революция', 2003)],
}

function stub(related: RelatedRows) {
  const f = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    if (url.endsWith('/api/items/5/related')) return json(related)
    if (url.endsWith('/api/items/5/collection')) return related.collection ? json(related.collection) : json({ error: 'Не найдено' }, 404)
    if (url.endsWith('/api/items/5')) return json(movie)
    return json({})
  })
  vi.stubGlobal('fetch', f)
  return f
}

function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/item/:id" element={<ItemDetail />} />
        <Route path="/item/:id/collection" element={<Collection />} />
      </Routes>
    </MemoryRouter>,
  )
}

describe('related rows of a title page', () => {
  beforeEach(() => session.reset())
  afterEach(() => vi.unstubAllGlobals())

  it('draws no row when there is nothing', async () => {
    const f = stub({ similar: [], collection: null })
    renderAt('/item/5')
    await screen.findByRole('heading', { name: 'Матрица', level: 1 })
    await vi.waitFor(() => expect(f.mock.calls.some(([u]) => String(u).endsWith('/related'))).toBe(true))
    expect(screen.queryByRole('region', { name: 'Похожие' })).not.toBeInTheDocument()
    expect(screen.queryByRole('region', { name: /Коллекция/ })).not.toBeInTheDocument()
  })

  it('the collection page lists every part', async () => {
    stub({ similar: [], collection })
    renderAt('/item/5/collection')
    expect(await screen.findByRole('heading', { name: 'Коллекция «Матрица»', level: 1 })).toBeInTheDocument()
    expect(screen.getByText('Четыре фильма о Нео')).toBeInTheDocument()
    expect(screen.getByText('3 фильма')).toBeInTheDocument()
    expect(screen.getByText('2 в медиатеке')).toBeInTheDocument()
    const grid = screen.getByRole('list', { name: 'Коллекция «Матрица»' })
    expect(within(grid).getAllByRole('link')).toHaveLength(3)
  })

  it('counts the library parts by id, not by onServer', async () => {
    // a library card the server marks off-server still counts
    const parts = [lib(5, 'Матрица', 1999, { onServer: false }), missing(605, 'Матрица: Революция', 2003)]
    stub({ similar: [], collection: { ...collection, parts } })
    renderAt('/item/5/collection')
    expect(await screen.findByText('1 в медиатеке')).toBeInTheDocument()
  })

  it('a collection that is not there says so', async () => {
    stub({ similar: [], collection: null })
    renderAt('/item/5/collection')
    expect(await screen.findByRole('heading', { name: 'Коллекция не найдена' })).toBeInTheDocument()
  })
})
