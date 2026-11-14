import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Card } from '../../api/types'
import { session } from '../../state/session'
import { Home } from '../Home'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const card = (over: Partial<Card>): Card => ({ id: 0, kind: 'series', title: '', subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: false, ...over })

describe('Главная: «Вышло новое»', () => {
  beforeEach(() => session.reset())
  afterEach(() => vi.unstubAllGlobals())

  it('shows the shelf: a library card links to its page, «Все» to the calendar', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json({ id: 1, name: 'Анна', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null })
        if (url.includes('/api/home'))
          return json({
            hero: [],
            shelves: [
              { id: 'continue', title: 'Продолжить', cards: [card({ id: 1, kind: 'movie', title: 'Дюна', onServer: true })] },
              {
                id: 'tracked-new',
                title: 'Вышло новое',
                cards: [
                  card({ id: 5, title: 'Тихая гавань', subtitle: 'С2 Э3', onServer: true }),
                  card({ title: 'Дальний берег', subtitle: 'С1 Э1', tmdbKind: 'series', tmdbId: 77, season: 1, episode: 1 }),
                ],
              },
            ],
          })
        return json({})
      }),
    )
    await session.login(1)
    render(
      <MemoryRouter>
        <Home />
      </MemoryRouter>,
    )
    expect(await screen.findByRole('heading', { name: 'Вышло новое' })).toBeTruthy()
    expect(screen.getByRole('link', { name: /Вышло новое/ }).getAttribute('href')).toBe('/calendar')
    expect(screen.getByRole('link', { name: /Тихая гавань/ }).getAttribute('href')).toBe('/item/5')
    // off the library: a TMDB page only in the sources build; the core build has none (not a link)
    const far = screen.queryByRole('link', { name: /Дальний берег/ })
    if (__LIBRARY_EDITION__) expect(far).toBeNull()
    else expect(far?.getAttribute('href')).toBe('/title/series/77?season=1&episode=1')
    expect(screen.getAllByText('Дальний берег').length).toBeGreaterThan(0)
  })
})
