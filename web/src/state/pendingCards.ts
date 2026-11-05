// Titles on their way into the library: the library pages show them at once
// as tinted cards with the download's progress (GET /api/library/pending).
// Asked on mount, every POLL_MS while something downloads, every IDLE_MS
// otherwise (someone else may start one), and at once when the library
// changes (an organized title turns into its library card). They follow
// «Моя медиатека» (state/libraryScope): there only the profile's own
// titles. Another build only: the hook is a constant [] in the core build.
import { useEffect, useState } from 'react'
import { getLibraryPending, useEvents } from '../api/client'
import type { Card, CardKind, Shelf } from '../api/types'
import { answerSlot, lastAnswer, rememberAnswer } from './lastAnswer'
import { useLibraryScope } from './libraryScope'
import { POLL_MS } from './useTitleProgress'

export const IDLE_MS = 15_000

const NONE: readonly Card[] = []

const busy = (c: Card) => c.pending?.status === 'active' || c.pending?.status === 'organizing'

/** Two answers draw the same cards (no re-render every poll of a paused download). */
function same(a: readonly Card[], b: readonly Card[]) {
  return (
    a.length === b.length &&
    a.every((x, i) => {
      const y = b[i]!
      return (
        x.id === y.id &&
        x.poster === y.poster &&
        x.pending?.key === y.pending?.key &&
        x.pending?.status === y.pending?.status &&
        x.pending?.done === y.pending?.done &&
        x.pending?.size === y.pending?.size &&
        x.pending?.stalled === y.pending?.stalled &&
        x.pending?.liveness === y.pending?.liveness &&
        x.pending?.trackerErrorKind === y.pending?.trackerErrorKind &&
        x.pending?.trackerError === y.pending?.trackerError
      )
    })
  )
}

export function usePendingCards(): readonly Card[] {
  const { scope } = useLibraryScope()
  // back on a library page: the last list at once (the cards stay with the shelves kept by state/lastAnswer)
  const [cards, setCards] = useState<readonly Card[]>(() => lastAnswer<readonly Card[]>(`pending ${scope}`) ?? NONE)
  const [kick, setKick] = useState(0)
  useEvents(() => setKick((k) => k + 1), ['library.updated'])

  useEffect(() => {
    if (__LIBRARY_EDITION__) return
    const ac = new AbortController()
    let timer: number | undefined
    const tick = async () => {
      window.clearTimeout(timer)
      let next = IDLE_MS
      if (!document.hidden) {
        try {
          const slot = answerSlot(`pending ${scope}`)
          const got = await getLibraryPending(ac.signal, scope)
          if (ac.signal.aborted) return
          const list = Array.isArray(got) ? got : NONE // a server without the endpoint
          rememberAnswer(slot, list)
          setCards((prev) => (same(prev, list) ? prev : list))
          if (list.some(busy)) next = POLL_MS
        } catch {
          if (ac.signal.aborted) return
          // a failed poll keeps the cards on screen; asked again later
        }
      }
      timer = window.setTimeout(tick, next)
    }
    const onVisible = () => {
      if (!document.hidden) void tick()
    }
    void tick()
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      ac.abort()
      window.clearTimeout(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [kick, scope])

  return cards
}

/**
 * The cards of a library list or shelf with the pending titles of `kinds`:
 * a title the library has already gets its progress on its card; the others
 * come first, as cards of their own.
 */
export function withPending(cards: Card[], pending: readonly Card[], kinds: readonly CardKind[]): Card[] {
  if (__LIBRARY_EDITION__ || pending.length === 0) return cards
  const mine = pending.filter((p) => kinds.includes(p.kind))
  if (mine.length === 0) return cards
  const onCard = new Map(mine.filter((p) => p.id > 0).map((p) => [p.id, p.pending]))
  const merged = onCard.size === 0 ? cards : cards.map((c) => (onCard.has(c.id) ? { ...c, pending: onCard.get(c.id) } : c))
  const own = mine.filter((p) => !(p.id > 0))
  return own.length === 0 ? merged : [...own, ...merged]
}

/** A shelf that shows the titles of `kinds` still downloading. */
export interface PendingShelf {
  id: string
  kinds: readonly CardKind[]
  /** The title of the shelf when the server sent none (nothing of the kinds in the library yet). */
  title: string
}

/**
 * Shelves with the pending titles merged into theirs (see withPending). A
 * shelf the server left out (empty) is added when it gets pending cards,
 * after the leading shelves named in `after` («Читаю сейчас»…).
 */
export function shelvesWithPending(shelves: Shelf[], pending: readonly Card[], spec: readonly PendingShelf[], after: readonly string[] = []): Shelf[] {
  if (__LIBRARY_EDITION__ || pending.length === 0) return shelves
  const out = shelves.map((s) => {
    const sp = spec.find((x) => x.id === s.id)
    return sp ? { ...s, cards: withPending(s.cards, pending, sp.kinds) } : s
  })
  let at = 0
  while (at < out.length && after.includes(out[at]!.id)) at++
  for (const sp of spec) {
    if (out.some((s) => s.id === sp.id)) {
      at = Math.max(at, out.findIndex((s) => s.id === sp.id) + 1)
      continue
    }
    const cards = withPending([], pending, sp.kinds)
    if (cards.length > 0) out.splice(at++, 0, { id: sp.id, title: sp.title, cards })
  }
  return out
}
