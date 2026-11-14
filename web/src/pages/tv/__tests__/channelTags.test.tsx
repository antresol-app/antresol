// The channel editor by tags: «Опишите канал» fills the tags and the name,
// chips turn on and off, the preview counts what would play, and the
// channel is saved with the tags.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ChannelEditor } from '../ChannelEditor'
import { decadeToggle } from '../ChannelTags'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

function server() {
  const sent: { rules: Record<string, unknown>; name: string }[] = []
  const previews: Record<string, unknown>[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/api/channels/options'))
        return json({
          genres: [{ key: 'комедия', name: 'Комедия' }, { key: 'драма', name: 'Драма' }],
          decades: [1960, 1970, 1980],
          titles: [],
          profiles: [],
          countries: [{ key: 'SU', count: 40 }, { key: 'FR', count: 3 }],
          languages: [{ key: 'ru', count: 41 }],
          keywords: [{ key: 'space', count: 5 }],
          aiAvailable: true,
        })
      if (url.endsWith('/api/channels/preview')) {
        const rules = (JSON.parse(String(init!.body)) as { rules: Record<string, unknown> }).rules
        previews.push(rules)
        const su = (rules.countries as string[] | undefined)?.includes('SU')
        return json({ movies: su ? 12 : 43, series: su ? 0 : 1, episodes: 0, items: [{ id: 7, kind: 'movie', title: 'Солярис', year: 1972 }] })
      }
      if (url.endsWith('/api/channels/suggest'))
        return json({ name: 'Советские комедии 70-х', via: 'ai', rules: { kinds: ['movie'], genres: ['комедия'], countries: ['SU'], yearFrom: 1970, yearTo: 1979 } })
      if (url.endsWith('/api/channels') && init?.method === 'POST') {
        sent.push(JSON.parse(String(init.body)))
        return json({ id: 1, number: 1, name: 'x', category: '', personal: false, auto: false, enabled: true, canEdit: true, logo: 0 }, 201)
      }
      return json({ error: 'unexpected ' + url }, 500)
    }),
  )
  return { sent, previews }
}

afterEach(() => vi.unstubAllGlobals())

describe('ChannelEditor by tags', () => {
  it('a sentence becomes tags and a name; the preview follows; the channel is saved with them', async () => {
    const { sent } = server()
    render(<ChannelEditor channel={null} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />)
    // the whole library at first
    expect(await screen.findByText('43 фильма · 1 сериал')).toBeInTheDocument()
    await userEvent.type(screen.getByLabelText('Опишите канал'), 'советские комедии 70-х')
    await userEvent.click(screen.getByRole('button', { name: 'Собрать' }))
    expect(await screen.findByText('Собрано с помощью ИИ — проверьте теги ниже')).toBeInTheDocument()
    expect(screen.getByLabelText('Название')).toHaveValue('Советские комедии 70-х')
    const countries = screen.getByRole('group', { name: 'Страны' })
    expect(within(countries).getByRole('button', { name: /СССР/ })).toHaveAttribute('aria-pressed', 'true')
    expect(within(screen.getByRole('group', { name: 'Жанры' })).getByRole('button', { name: 'Комедия' })).toHaveAttribute('aria-pressed', 'true')
    expect(within(screen.getByRole('group', { name: 'Годы' })).getByRole('button', { name: '1970-е' })).toHaveAttribute('aria-pressed', 'true')
    expect(within(screen.getByRole('group', { name: 'Что' })).getByRole('button', { name: 'Фильмы' })).toHaveAttribute('aria-pressed', 'true')
    expect(await screen.findByText('12 фильмов')).toBeInTheDocument()
    // a theme by hand, and the 80s too (the range grows)
    await userEvent.click(within(screen.getByRole('group', { name: 'Темы' })).getByRole('button', { name: /Космос/ }))
    await userEvent.click(within(screen.getByRole('group', { name: 'Годы' })).getByRole('button', { name: '1980-е' }))
    await userEvent.click(screen.getByRole('button', { name: 'Создать' }))
    await waitFor(() => expect(sent).toHaveLength(1))
    expect(sent[0]!.name).toBe('Советские комедии 70-х')
    expect(sent[0]!.rules).toMatchObject({ kinds: ['movie'], genres: ['комедия'], countries: ['SU'], keywords: ['space'], yearFrom: 1970, yearTo: 1989 })
  })

  it('a name typed by hand is kept when the sentence changes', async () => {
    server()
    render(<ChannelEditor channel={null} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />)
    await userEvent.type(await screen.findByLabelText('Название'), 'Мой канал')
    await userEvent.type(screen.getByLabelText('Опишите канал'), 'советские комедии')
    await userEvent.click(screen.getByRole('button', { name: 'Собрать' }))
    await screen.findByText('Собрано с помощью ИИ — проверьте теги ниже')
    expect(screen.getByLabelText('Название')).toHaveValue('Мой канал')
  })
})

describe('decadeToggle', () => {
  it('picks, widens, narrows and clears a range of years', () => {
    expect(decadeToggle(undefined, undefined, 1970)).toEqual({ yearFrom: 1970, yearTo: 1979 })
    expect(decadeToggle(1970, 1979, 1980)).toEqual({ yearFrom: 1970, yearTo: 1989 })
    expect(decadeToggle(1970, 1979, 1960)).toEqual({ yearFrom: 1960, yearTo: 1979 })
    expect(decadeToggle(1960, 1989, 1970)).toEqual({ yearFrom: 1970, yearTo: 1979 })
    expect(decadeToggle(1970, 1979, 1970)).toEqual({})
    expect(decadeToggle(1950, 1991, 1990)).toEqual({ yearFrom: 1990, yearTo: 1999 })
  })
})
