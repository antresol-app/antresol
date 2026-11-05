// Главная drawn from a kept answer (state/lastAnswer) never draws what
// carries a position: «Продолжить» (an online card's link resumes at its
// second), «Смотрят дома» (another profile's progress, which they may have
// hidden since). Those rows come only with the fresh answer.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Card } from '../../api/types'
import { session } from '../../state/session'
import { lastAnswer } from '../../state/lastAnswer'
import { Home } from '../Home'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const card = (id: number, title: string, o: Partial<Card> = {}): Card => ({ id, kind: 'movie', title, subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: true, ...o })

const home = (resumeSec: number) => ({
  hero: [],
  shelves: [
    {
      id: 'continue',
      title: 'Продолжить просмотр',
      cards: [card(0, 'Онлайн-фильм', { online: true, tmdbKind: 'movie', tmdbId: 77, resumeSec, onServer: false })],
    },
    { id: 'family', title: 'Смотрят дома', cards: [card(5, 'Фильм Ани', { progress: 0.4 })] },
    { id: 'recent-movies', title: 'Недавно добавленные фильмы', cards: [card(1, 'Дюна')] },
  ],
})

describe('Главная from a kept answer', () => {
  let answers: ((body: unknown) => void)[]
  beforeEach(async () => {
    session.reset()
    answers = []
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/api/login')) return json({ id: 1, name: 'Саша', avatar: 'grad-1', hasPassword: true, isAdmin: false, isKid: false })
        if (url.startsWith('/api/home')) return new Promise<Response>((resolve) => answers.push((b) => resolve(json(b))))
        if (url.includes('/api/library/pending')) return json([])
        return json({})
      }),
    )
    await session.login(1)
  })
  afterEach(() => vi.unstubAllGlobals())

  const show = () =>
    render(
      <MemoryRouter>
        <Home />
      </MemoryRouter>,
    )

  it('leaves out «Продолжить» and «Смотрят дома» until the fresh answer (a library card)', async () => {
    const library = {
      hero: [],
      shelves: [
        { id: 'continue', title: 'Продолжить просмотр', cards: [card(3, 'Начатый фильм', { progress: 0.3 })] },
        { id: 'family', title: 'Смотрят дома', cards: [card(5, 'Фильм Ани', { progress: 0.4 })] },
        { id: 'recent-movies', title: 'Недавно добавленные фильмы', cards: [card(1, 'Дюна')] },
      ],
    }
    const first = show()
    answers[0]!(library)
    expect(await screen.findByText('Продолжить просмотр')).toBeInTheDocument()
    expect(screen.getAllByText('Начатый фильм')[0]).toBeInTheDocument()
    first.unmount()

    // the kept answer: the library's shelves only
    show()
    expect(screen.getAllByText('Дюна')[0]).toBeInTheDocument()
    expect(screen.queryByText('Продолжить просмотр')).not.toBeInTheDocument()
    expect(screen.queryAllByText('Начатый фильм')).toHaveLength(0)
    expect(screen.queryByText('Смотрят дома')).not.toBeInTheDocument()
    expect(screen.queryAllByText('Фильм Ани')).toHaveLength(0)
    await vi.waitFor(() => expect(answers).toHaveLength(2))
    answers[1]!(library)
    expect(await screen.findByText('Продолжить просмотр')).toBeInTheDocument()
    expect(screen.getByText('Смотрят дома')).toBeInTheDocument()
  })

  it('a change of the profile’s «hide progress» forgets the kept answers', async () => {
    const first = show()
    answers[0]!(home(600))
    expect(await screen.findByRole('link', { name: /Онлайн-фильм/ })).toBeInTheDocument()
    first.unmount()
    expect(lastAnswer('home all')).toBeDefined()
    session.updateUser({ id: 1, hideProgress: true })
    expect(lastAnswer('home all')).toBeUndefined()
  })
})
