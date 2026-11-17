// Pending cards (GET /api/library/pending, the addons' library.pending):
// the same in every build — the card, its ring and words, where it opens,
// and how the list merges into the library's.
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Card as CardData, PendingState, Shelf } from '../../api/types'
import { Card } from '../Card'
import { PendingOverlay, pendingLabel } from '../PendingOverlay'
import { PosterGrid } from '../PosterGrid'
import { t } from '../../i18n'
import { shelvesWithPending, withPending } from '../../state/pendingCards'
import { admin, card } from './fixtures'

const who = vi.hoisted(() => ({ user: null as unknown }))
vi.mock('../../state/session', () => ({
  useSession: () => ({ status: 'signed-in', user: who.user }),
  // state/lastAnswer (through pendingCards) follows the signed-in profile
  session: { get: () => ({ status: 'signed-in', user: who.user }), subscribe: () => () => {} },
}))

/** «Загружается 40 %» as the label has it (a no-break space before the sign). */
const active40 = 'Загружается 40\u00a0%'

const state = (over: Partial<PendingState> = {}): PendingState => ({ key: 'dl:movie:7', status: 'active', done: 40, size: 100, ...over })

/** A title's own card, as GET /api/library/pending sends it. */
const own = (over: Partial<CardData> = {}): CardData =>
  card(0, { title: 'Дюна', onServer: false, tmdbKind: 'movie', tmdbId: 7, pending: state({ href: '/title/movie/7' }), ...over })

const show = (c: CardData) =>
  render(
    <MemoryRouter>
      <Card card={c} />
    </MemoryRouter>,
  )

beforeEach(() => {
  who.user = { ...admin, isAdmin: false }
})

describe('pending card', () => {
  it('is tinted, opens its href and draws the ring', () => {
    show(own())
    const link = screen.getByRole('link', { name: /Дюна/ })
    expect(link).toHaveAttribute('href', '/title/movie/7')
    expect(link.className).toMatch(/pending/)
    expect(within(link).getByText('Загружается 40 %')).toBeInTheDocument()
    // the ring says what the subtitle says: hidden, so it is not read twice
    const ring = within(link).getByRole('progressbar', { hidden: true, name: active40 })
    expect(ring).toHaveAttribute('aria-valuenow', '40')
    expect(within(link).queryByRole('progressbar')).not.toBeInTheDocument()
    expect(within(link).getAllByRole('progressbar', { hidden: true })).toHaveLength(1)
    expect(within(link).queryByTitle('Ещё не на сервере')).not.toBeInTheDocument()
  })

  it('a pending card opens its href, none without', () => {
    const { unmount } = show(own({ kind: 'album', title: 'Группа крови', tmdbKind: undefined, tmdbId: undefined, pending: state({ key: 'dl:music:x' }) }))
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.getByRole('group', { name: `Группа крови, ${active40}` })).toBeInTheDocument()
    unmount()
    // the addon names the page; an admin profile changes nothing by itself
    who.user = admin
    show(own({ kind: 'album', title: 'Группа крови', tmdbKind: undefined, tmdbId: undefined, pending: state({ key: 'dl:music:x', href: '/addons/dl/downloads' }) }))
    expect(screen.getByRole('link', { name: /Группа крови/ })).toHaveAttribute('href', '/addons/dl/downloads')
  })

  it.each([
    [{ status: 'paused' as const }, 'Пауза · 40 %'],
    [{ status: 'waiting' as const }, 'Ждёт места на диске'],
    [{ status: 'organizing' as const, done: 100 }, 'Добавляется в медиатеку'],
    [{ size: 0, done: 0 }, 'Загружается'],
    // an active one without headway says why, in the addon's words
    [{ hint: 'Ждём источник…', done: 0 }, 'Ждём источник…'],
    [{ status: 'paused' as const, hint: 'Ждём источник…' }, 'Пауза · 40 %'],
  ])('reads its status %o as «%s»', (over, text) => {
    show(own({ pending: state(over) }))
    expect(screen.getByText(text)).toBeInTheDocument()
  })

  it('shows a failure without a ring', () => {
    show(own({ pending: state({ status: 'error' }) }))
    expect(screen.getByText('Не удалось загрузить')).toBeInTheDocument()
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  })

  it('on a library card: a small ring, the card stays as it is', () => {
    show(card(900, { kind: 'series', title: 'Сёгун', subtitle: 'Драма', pending: state({ key: 'dl:series:42' }) }))
    const link = screen.getByRole('link', { name: /Сёгун/ })
    expect(link).toHaveAttribute('href', '/item/900')
    expect(link.className).not.toMatch(/pending/)
    expect(within(link).getByText('Драма')).toBeInTheDocument()
    expect(within(link).getByRole('progressbar', { name: active40 })).toBeInTheDocument()
  })

  it('pending cards of id 0 get keys of their own in a grid', () => {
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    render(
      <MemoryRouter>
        <PosterGrid cards={[own(), own({ title: 'Брат', tmdbId: 8, pending: state({ key: 'dl:movie:8' }) }), card(1)]} label="Фильмы" />
      </MemoryRouter>,
    )
    expect(screen.getAllByRole('listitem')).toHaveLength(3)
    expect(errors).not.toHaveBeenCalled()
    errors.mockRestore()
  })
})

describe('PendingOverlay', () => {
  it('the hint wins over the percent for an active one; an error has its icon and no ring', () => {
    expect(pendingLabel(t, state({ hint: 'Ждём источник…' }))).toBe('Ждём источник…')
    expect(pendingLabel(t, state())).toBe(active40)
    const { container } = render(<PendingOverlay pending={state({ status: 'error' })} />)
    expect(container.querySelector('svg')).not.toBeNull()
    expect(screen.queryByRole('progressbar', { hidden: true })).not.toBeInTheDocument()
  })
})

describe('withPending', () => {
  const lib = [card(1), card(900, { kind: 'series' })]
  const pending = [own(), own({ id: 900, kind: 'series', pending: state({ key: 'dl:series:42', done: 5 }) }), own({ kind: 'album', pending: state({ key: 'dl:music:x' }) })]

  it('leaves other kinds out', () => {
    expect(withPending(lib, pending, ['book'])).toBe(lib)
    expect(withPending(lib, [], ['movie'])).toBe(lib)
  })

  it('merges pending cards in the core build: new titles first, the progress on the library card', () => {
    const got = withPending(lib, pending, ['movie', 'series'])
    expect(got.map((c) => `${c.kind}-${c.id}`)).toEqual(['movie-0', 'movie-1', 'series-900'])
    expect(got[2]!.pending?.done).toBe(5)
    expect(got[1]!.pending).toBeUndefined()
  })

  it('adds a shelf the server left out, after the reading ones', () => {
    const shelves: Shelf[] = [
      { id: 'reading', title: 'Читаю сейчас', cards: [card(5, { kind: 'book' })] },
      { id: 'done', title: 'Прочитано', cards: [card(6, { kind: 'book' })] },
    ]
    const book = own({ kind: 'book', title: 'Мастер и Маргарита', pending: state({ key: 'dl:book:x' }) })
    const got = shelvesWithPending(shelves, [book], [{ id: 'recent', kinds: ['book'], title: 'Недавно добавленные' }], ['reading', 'want'])
    expect(got.map((s) => s.id)).toEqual(['reading', 'recent', 'done'])
    expect(got[1]!.cards).toEqual([book])
  })
})
