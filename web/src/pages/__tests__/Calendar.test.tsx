import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { Calendar } from '../Calendar'
import { dayCards } from '../calendarCards'
import { t } from '../../i18n'
import type { TrackingItem } from '../../api/types'

const json = (b: unknown) =>
  new Response(JSON.stringify(b), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
const title = (name: string, itemId?: number) => ({
  kind: 'series' as const,
  tmdbId: itemId ? 7 : 1,
  name,
  poster: `/t/${name.length}.jpg`,
  status: 'Returning Series',
  ...(itemId ? { itemId } : {}),
})

let urls: string[]
beforeEach(() => {
  // Friday 13 Nov 2026: the week is 9–15 Nov; the month's grid 26 Oct – 6 Dec
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
            items: [
              {
                title: title('Тихая гавань', 7),
                season: 2,
                episode: 3,
                name: 'Прилив',
                airDate: '2026-11-12',
                firstSeen: '2026-11-12T20:00:00Z',
                kinds: ['library'],
              },
            ],
          },
          {
            date: '2026-11-14',
            items: [
              {
                title: title('Дальний берег'),
                season: 1,
                episode: 1,
                name: '',
                airDate: '2026-11-14',
                still: 'https://image.example/w300/still-1x1.jpg',
                firstSeen: null,
                kinds: [],
              },
            ],
          },
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

describe('Календарь: неделя', () => {
  it('a row of covers per day, «доступно» only for what has a source', async () => {
    show()
    expect(await screen.findByRole('heading', { name: 'Четверг, 12 ноября' })).toBeTruthy()
    expect(urls[0]).toBe('/api/tracking/calendar?from=2026-11-09&to=2026-11-15')
    expect(screen.getByRole('heading', { name: 'Суббота, 14 ноября' })).toBeTruthy()
    const lib = screen.getByRole('link', { name: /Тихая гавань/ })
    expect(lib.getAttribute('href')).toBe('/item/7')
    expect(within(lib).getByText('С2 Э3')).toBeTruthy()
    // the cover is drawn
    expect(lib.querySelector('img')?.getAttribute('src')).toContain('/t/12.jpg')
    expect(screen.getAllByText('доступно')).toHaveLength(1)
  })

  it('an episode with a still shows it, one without the poster', async () => {
    show()
    const off = await screen.findByRole('link', { name: /Дальний берег/ })
    expect(off.querySelector('img')?.getAttribute('src')).toContain('still-1x1.jpg')
    expect(screen.getByRole('link', { name: /Тихая гавань/ }).querySelector('img')?.getAttribute('src')).toContain('/t/12.jpg')
  })

  it('an off-library title opens its title page on that episode', async () => {
    show()
    expect(await screen.findByRole('link', { name: /Дальний берег/ })).toHaveAttribute('href', '/title/series/1?season=1&episode=1')
  })

  it('«Позже», «Раньше» and «Сегодня» move the week, «Месяц» asks for the month grid', async () => {
    show()
    await screen.findByText('Тихая гавань')
    const today = screen.getByRole('button', { name: 'Сегодня' })
    expect(today).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Позже' }))
    await waitFor(() => expect(urls.at(-1)).toBe('/api/tracking/calendar?from=2026-11-16&to=2026-11-22'))
    expect(today).toBeEnabled()
    await userEvent.click(screen.getByRole('button', { name: 'Раньше' }))
    await userEvent.click(screen.getByRole('button', { name: 'Раньше' }))
    await waitFor(() => expect(urls.at(-1)).toBe('/api/tracking/calendar?from=2026-11-02&to=2026-11-08'))
    await userEvent.click(today)
    await waitFor(() => expect(urls.at(-1)).toBe('/api/tracking/calendar?from=2026-11-09&to=2026-11-15'))
    await userEvent.click(screen.getByRole('radio', { name: 'Месяц' }))
    await waitFor(() => expect(urls.at(-1)).toBe('/api/tracking/calendar?from=2026-10-26&to=2026-12-06'))
  })
})

describe('Календарь: месяц', () => {
  it('six weeks of seven days, today marked, a day with covers; a tap shows its row', async () => {
    show()
    await userEvent.click(screen.getByRole('radio', { name: 'Месяц' }))
    await waitFor(() => expect(urls.at(-1)).toBe('/api/tracking/calendar?from=2026-10-26&to=2026-12-06'))
    const days = await screen.findAllByRole('button', { pressed: false })
    // 42 cells: one pressed (today), 41 not
    expect(days).toHaveLength(41)
    const today = screen.getByRole('button', { pressed: true })
    expect(today).toHaveAttribute('aria-current', 'date')
    expect(today.textContent).toBe('13')
    // today has nothing
    expect(await screen.findByText('В этот день ничего не выходит.')).toBeTruthy()
    // the 12th has a cover and a name
    const twelfth = screen.getByRole('button', {
      name: /12 ноября: Тихая гавань/,
    })
    expect(twelfth.querySelector('img')?.getAttribute('src')).toContain('/t/12.jpg')
    expect(within(twelfth).getByText('С2 Э3')).toBeTruthy()
    // the 14th: the episode's still, not the series' poster
    const fourteenth = screen.getByRole('button', { name: /14 ноября: Дальний берег/ })
    expect(fourteenth.querySelector('img')?.getAttribute('src')).toContain('still-1x1.jpg')
    await userEvent.click(twelfth)
    expect(twelfth).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('heading', { name: 'Четверг, 12 ноября' })).toBeTruthy()
    expect(screen.getByRole('link', { name: /Тихая гавань/ })).toHaveAttribute('href', '/item/7')
    // the days of the months around are there (dimmed by style)
    expect(screen.getByRole('button', { name: /^понедельник, 26 октября$/ })).toBeTruthy()
  })
})

describe('dayCards', () => {
  const ep = (season: number, episode: number, firstSeen: string | null = null): TrackingItem => ({
    title: { kind: 'series', tmdbId: 9, name: 'Ночь', poster: '', status: '' },
    season,
    episode,
    name: '',
    firstSeen,
    kinds: [],
  })
  it('one card per title a day: a season span, or the first and how many more', () => {
    expect(dayCards(t, [ep(1, 1), ep(1, 2), ep(1, 3, 'x')]).map((c) => [c.subtitle, c.badges])).toEqual([['С1 Э1–3', ['доступно']]])
    expect(dayCards(t, [ep(1, 8), ep(2, 1)]).map((c) => c.subtitle)).toEqual(['С1 Э8 +1'])
  })
})
