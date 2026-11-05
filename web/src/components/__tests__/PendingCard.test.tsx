import { describe, expect, it, vi, beforeEach } from 'vitest'
import type { Card as CardData, PendingState } from '../../api/types'
import { withPending } from '../../state/pendingCards'
import { admin, card } from './fixtures'

const who = vi.hoisted(() => ({ user: null as unknown }))
vi.mock('../../state/session', () => ({
  useSession: () => ({ status: 'signed-in', user: who.user }),
  // state/lastAnswer (through pendingCards) follows the signed-in profile
  session: { get: () => ({ status: 'signed-in', user: who.user }), subscribe: () => () => {} },
}))

const state = (over: Partial<PendingState> = {}): PendingState => ({ key: 'movie:7', status: 'active', done: 40, size: 100, ...over })

/** A download's own card, as GET /api/library/pending sends it. */
const own = (over: Partial<CardData> = {}): CardData =>
  card(0, { title: 'Дюна', onServer: false, tmdbKind: 'movie', tmdbId: 7, pending: state(), ...over })

beforeEach(() => {
  who.user = { ...admin, isAdmin: false }
})

describe('withPending', () => {
  const lib = [card(1), card(900, { kind: 'series' })]
  const pending = [own(), own({ id: 900, kind: 'series', pending: state({ key: 'series:42', done: 5 }) }), own({ kind: 'album', pending: state({ key: 'music:x' }) })]

  it('leaves other kinds out', () => {
    expect(withPending(lib, pending, ['book'])).toBe(lib)
    expect(withPending(lib, [], ['movie'])).toBe(lib)
  })
})
