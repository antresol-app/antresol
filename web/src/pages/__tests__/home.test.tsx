import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Card } from '../../api/types'
import { session } from '../../state/session'
import { Home } from '../Home'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const card = (id: number, kind: string, title: string): Card => ({
  id,
  kind,
  title,
  subtitle: '',
  year: 2020,
  poster: '',
  backdrop: '',
  logo: '',
  progress: 0,
  onServer: true,
})

describe('Главная', () => {
  beforeEach(() => session.reset())
  afterEach(() => vi.unstubAllGlobals())

  it('renders Слушали недавно with square cards', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json({ id: 1, name: 'Александр', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null })
        if (url.includes('/api/home'))
          return json({
            hero: [],
            shelves: [
              { id: 'continue', title: 'Продолжить', cards: [card(1, 'movie', 'Дюна')] },
              { id: 'recently-played', title: 'Слушали недавно', cards: [card(10, 'album', 'Группа крови'), card(11, 'playlist', 'В дорогу')] },
              { id: 'reading', title: 'Читаю сейчас', cards: [card(20, 'book', 'Мастер и Маргарита')] },
              { id: 'recent-movies', title: 'Недавно добавленные фильмы', cards: [] },
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
    const played = await screen.findByRole('region', { name: /Слушали недавно/ })
    const links = within(played).getAllByRole('link').filter((a) => a.getAttribute('href')?.startsWith('/music/'))
    expect(links.map((a) => a.getAttribute('href'))).toEqual(['/music/album/10', '/music/playlist/11'])
    for (const a of links) expect(a).toHaveClass('square')
    // «Все» goes to the section
    expect(within(played).getByRole('link', { name: /Слушали недавно/ })).toHaveAttribute('href', '/music')
    const reading = screen.getByRole('region', { name: /Читаю сейчас/ })
    expect(within(reading).getByRole('link', { name: /Читаю сейчас/ })).toHaveAttribute('href', '/books')
    expect(within(reading).getByRole('link', { name: /Мастер и Маргарита/ })).toHaveClass('poster')
    // after «Продолжить», in the server's order
    const titles = screen.getAllByRole('heading', { level: 2 }).map((h) => h.textContent)
    expect(titles).toEqual(['Продолжить', 'Слушали недавно', 'Читаю сейчас'])
  })
})
