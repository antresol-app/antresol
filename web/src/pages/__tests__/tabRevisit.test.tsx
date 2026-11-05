// Going back to a tab (Главная, Музыка, Книги) draws its last answer at once
// and swaps in the fresh one when it comes; another profile never sees it.
// A kept answer whose refresh fails, or that is older than KEEP_MS, is not
// shown as if it were current.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { ComponentType } from 'react'
import type { Card } from '../../api/types'
import { session } from '../../state/session'
import { KEEP_MS, lastAnswer } from '../../state/lastAnswer'
import { Home } from '../Home'
import { MusicHome } from '../music/MusicHome'
import { BooksHome } from '../books/BooksHome'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const card = (id: number, kind: string, title: string): Card => ({ id, kind, title, subtitle: '', year: 2020, poster: '', backdrop: '', logo: '', progress: 0, onServer: true })

const TABS: { name: string; Page: ComponentType; path: string; kind: string; shelf: string }[] = [
  { name: 'Главная', Page: Home, path: '/api/home', kind: 'movie', shelf: 'recent-movies' },
  { name: 'Музыка', Page: MusicHome, path: '/api/music/home', kind: 'album', shelf: 'recent-albums' },
  { name: 'Книги', Page: BooksHome, path: '/api/books/home', kind: 'book', shelf: 'recent' },
]

describe.each(TABS)('$name: back to the tab', ({ Page, path, kind, shelf }) => {
  /** The tab's answers, released by the test. */
  let answers: ((title: string) => void)[]
  let failures: (() => void)[]
  beforeEach(async () => {
    session.reset()
    answers = []
    failures = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json({ id: (JSON.parse(String(init?.body)) as { userId: number }).userId, name: 'Саша', avatar: 'grad-1', hasPassword: true, isAdmin: false, isKid: false })
        if (url.startsWith(path)) {
          return new Promise<Response>((resolve) => {
            answers.push((title) => resolve(json({ hero: [], shelves: [{ id: shelf, title: 'Полка', cards: [card(1, kind, title)] }] })))
            failures.push(() => resolve(new Response(JSON.stringify({ error: 'Сервер недоступен' }), { status: 503 })))
          })
        }
        if (url.includes('/api/library/pending')) return json([])
        if (url.includes('/api/music/genres')) return json({ genres: [{ name: 'Рок', albums: 3, songs: 30 }] })
        return json({})
      }),
    )
    await session.login(1)
  })
  afterEach(() => vi.unstubAllGlobals())

  const show = () =>
    render(
      <MemoryRouter>
        <Page />
      </MemoryRouter>,
    )

  it('draws the last answer at once, asks again and shows the new one', async () => {
    const first = show()
    answers[0]!('Первый ответ')
    expect((await screen.findAllByText('Первый ответ'))[0]).toBeInTheDocument()
    first.unmount()

    show()
    expect(screen.getAllByText('Первый ответ')[0]).toBeInTheDocument() // no skeleton round
    expect(screen.queryByLabelText('Загрузка')).not.toBeInTheDocument()
    await vi.waitFor(() => expect(answers).toHaveLength(2))
    answers[1]!('Второй ответ')
    expect((await screen.findAllByText('Второй ответ'))[0]).toBeInTheDocument()
  })

  it('another profile starts from nothing', async () => {
    const first = show()
    answers[0]!('Чужая полка')
    expect((await screen.findAllByText('Чужая полка'))[0]).toBeInTheDocument()
    first.unmount()

    session.reset()
    await session.login(2)
    show()
    expect(screen.queryAllByText('Чужая полка')).toHaveLength(0)
  })

  it('a refresh of the kept answer that fails shows the error, as a first visit would, and forgets it', async () => {
    const first = show()
    answers[0]!('Старая полка')
    expect((await screen.findAllByText('Старая полка'))[0]).toBeInTheDocument()
    first.unmount()

    const second = show()
    expect(screen.getAllByText('Старая полка')[0]).toBeInTheDocument()
    await vi.waitFor(() => expect(failures).toHaveLength(2))
    failures[1]!()
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(screen.queryAllByText('Старая полка')).toHaveLength(0)
    second.unmount()

    show() // nothing kept any more: the skeleton, not the old shelves
    expect(screen.queryAllByText('Старая полка')).toHaveLength(0)
  })

  it('an answer kept longer than KEEP_MS is not drawn', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    try {
      const first = show()
      answers[0]!('Давняя полка')
      expect((await screen.findAllByText('Давняя полка'))[0]).toBeInTheDocument()
      first.unmount()
      vi.setSystemTime(Date.now() + KEEP_MS + 1)
      show()
      expect(screen.queryAllByText('Давняя полка')).toHaveLength(0)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('Музыка: the genre row', () => {
  it('is kept with the shelves', async () => {
    session.reset()
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json({ id: 1, name: 'Саша', avatar: 'grad-1', isAdmin: false, isKid: false })
        if (url.includes('/api/music/genres')) return json({ genres: [{ name: 'Рок', albums: 3, songs: 30 }] })
        if (url.startsWith('/api/music/home')) return json({ shelves: [{ id: 'recent-albums', title: 'Полка', cards: [card(1, 'album', 'Альбом')] }] })
        return json([])
      }),
    )
    try {
      await session.login(1)
      render(
        <MemoryRouter>
          <MusicHome />
        </MemoryRouter>,
      )
      await vi.waitFor(() => expect(lastAnswer('music genres')).toEqual([{ name: 'Рок', albums: 3, songs: 30 }]))
    } finally {
      vi.unstubAllGlobals()
    }
  })
})
