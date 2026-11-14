import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { Calendar } from '../Calendar'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const title = (name: string, itemId?: number) => ({ kind: 'series', tmdbId: 1, name, poster: '', status: 'Returning Series', ...(itemId ? { itemId } : {}) })

let urls: string[]
beforeEach(() => {
  // Friday 13 Nov 2026: the week is 9–15 Nov, the month 1–30 Nov
  vi.useFakeTimers({ toFake: ['Date'], now: new Date(2026, 10, 13, 12) })
  urls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      urls.push(String(input))
      return json({
        days: [
          {
            date: '2026-11-12',
            items: [{ title: title('Тихая гавань', 7), season: 2, episode: 3, name: 'Прилив', airDate: '2026-11-12', firstSeen: '2026-11-12T20:00:00Z', kinds: ['library'] }],
          },
          { date: '2026-11-14', items: [{ title: title('Дальний берег'), season: 1, episode: 1, name: '', airDate: '2026-11-14', firstSeen: null, kinds: [] }] },
        ],
      })
    }),
  )
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

const show = () =>
  render(
    <MemoryRouter>
      <Calendar />
    </MemoryRouter>,
  )

describe('Календарь', () => {
  it('shows the days of the week and «доступно» only for what has a source', async () => {
    show()
    expect(await screen.findByText('Тихая гавань')).toBeTruthy()
    expect(urls[0]).toBe('/api/tracking/calendar?from=2026-11-09&to=2026-11-15')
    expect(screen.getByText('Дальний берег')).toBeTruthy()
    expect(screen.getByText('С2 Э3 · Прилив')).toBeTruthy()
    expect(screen.getAllByText('доступно')).toHaveLength(1)
    // a library title links to its page
    expect(screen.getByRole('link', { name: /Тихая гавань/ }).getAttribute('href')).toBe('/item/7')
  })

  it('«Позже» and «Раньше» move the week, «Месяц» widens it', async () => {
    show()
    await screen.findByText('Тихая гавань')
    await userEvent.click(screen.getByRole('button', { name: 'Позже' }))
    await waitFor(() => expect(urls.at(-1)).toBe('/api/tracking/calendar?from=2026-11-16&to=2026-11-22'))
    await userEvent.click(screen.getByRole('button', { name: 'Раньше' }))
    await userEvent.click(screen.getByRole('button', { name: 'Раньше' }))
    await waitFor(() => expect(urls.at(-1)).toBe('/api/tracking/calendar?from=2026-11-02&to=2026-11-08'))
    await userEvent.click(screen.getByRole('radio', { name: 'Месяц' }))
    await waitFor(() => expect(urls.at(-1)).toBe('/api/tracking/calendar?from=2026-11-01&to=2026-11-30'))
  })
})
