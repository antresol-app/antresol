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
    // off the library: its core title page, on the new episode
    expect(screen.getByRole('link', { name: /Дальний берег/ }).getAttribute('href')).toBe('/title/series/77?season=1&episode=1')
    expect(screen.getAllByText('Дальний берег').length).toBeGreaterThan(0)
  })

  it('«Скоро выйдет»: the next episode of each followed title from today, a cover each, its title opens the calendar', async () => {
    vi.useFakeTimers({ toFake: ['Date'], now: new Date(2026, 10, 13, 12) })
    const asked: string[] = []
    const gavan = { kind: 'series', tmdbId: 7, itemId: 5, name: 'Тихая гавань', poster: '/p.jpg', status: '' }
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        asked.push(url)
        if (url.endsWith('/api/login')) return json({ id: 1, name: 'Анна', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null })
        if (url.includes('/api/home')) return json({ hero: [], shelves: [{ id: 'continue', title: 'Продолжить', cards: [card({ id: 1, kind: 'movie', title: 'Дюна', onServer: true })] }] })
        if (url.includes('/api/tracking/calendar'))
          return json({
            days: [
              { date: '2026-11-13', items: [{ title: gavan, season: 2, episode: 3, name: '', firstSeen: null, kinds: [] }] },
              {
                date: '2026-11-20',
                items: [
                  { title: gavan, season: 2, episode: 4, name: '', firstSeen: null, kinds: [] },
                  { title: { kind: 'series', tmdbId: 8, name: 'Дальний берег', poster: '/q.jpg', status: '' }, season: 1, episode: 1, name: '', firstSeen: null, kinds: [] },
                ],
              },
            ],
          })
        return json({})
      }),
    )
    try {
      await session.login(1)
      render(
        <MemoryRouter>
          <Home />
        </MemoryRouter>,
      )
      expect(await screen.findByRole('link', { name: /Скоро выйдет/ })).toHaveAttribute('href', '/calendar')
      expect(asked).toContain('/api/tracking/calendar?from=2026-11-13&to=2026-12-13')
      // one card per title: its soonest episode
      const link = screen.getByRole('link', { name: /Тихая гавань/ })
      expect(link).toHaveAttribute('href', '/item/5')
      expect(link.textContent).toContain('С2 Э3 · Сегодня')
      expect(link.querySelector('img')).toBeTruthy()
      expect(screen.getByRole('link', { name: /Дальний берег/ })).toHaveAttribute('href', '/title/series/8?season=1&episode=1')
    } finally {
      vi.useRealTimers()
    }
  })
})
