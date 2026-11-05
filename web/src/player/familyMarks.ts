// «Семья» on a timeline: where the other profiles are in what plays or is
// read now, as small faces on the scrubber (or the reader's slider), a jump
// to one of them and «↩ Вернуться к …» — back to where this profile was.
// The pure parts live here (placement, what counts as a place, the held
// position while a jump is only looked at); FamilyMarks draws them.
import { useCallback, useEffect, useRef, useState } from 'react'
import type { FamilyMember } from '../api/types'

/** One profile's place on a timeline, in the timeline's own units (seconds, ms, pages, 0..1). */
export interface FamilyPlace {
  member: FamilyMember
  at: number
  /** «34:12», «стр. 120», «42 %». */
  label: string
}

/** A place laid out on the track. */
export interface PlacedMark extends FamilyPlace {
  /** 0..100 along the track. */
  pct: number
  /** Pixels to the side of pct, so faces at (nearly) the same spot stay apart. */
  offset: number
}

/** Size of a face on the track (px) and how far apart faces of one cluster sit. */
export const MARK_SIZE = 20
const SPREAD = 0.75

/**
 * Lays out places on a track `trackPx` wide for a timeline of `total`:
 * percent along it (clamped to 0..100), and faces closer than one face
 * apart grouped into a cluster that fans out around its middle.
 */
export function placeMarks(places: FamilyPlace[], total: number, trackPx: number, size = MARK_SIZE): PlacedMark[] {
  if (!(total > 0) || places.length === 0) return []
  const placed = places
    .filter((p) => Number.isFinite(p.at))
    .map((p) => ({ ...p, pct: Math.min(100, Math.max(0, (p.at / total) * 100)), offset: 0 }))
    .sort((a, b) => a.pct - b.pct || a.member.userId - b.member.userId)
  const gap = trackPx > 0 ? (size / trackPx) * 100 : 0
  const out: PlacedMark[] = []
  let cluster: PlacedMark[] = []
  const flush = () => {
    if (cluster.length === 0) return
    const mid = cluster.reduce((a, m) => a + m.pct, 0) / cluster.length
    const step = size * SPREAD
    cluster.forEach((m, i) => out.push({ ...m, pct: mid, offset: (i - (cluster.length - 1) / 2) * step }))
    cluster = []
  }
  for (const m of placed) {
    const last = cluster[cluster.length - 1]
    if (last && m.pct - last.pct >= gap) flush()
    cluster.push(m)
  }
  flush()
  return out
}

/**
 * Whether m has a place to jump to in item `itemId`: started there and not
 * finished (a finished profile is «at the end», nothing to look at).
 */
export const hasPlace = (m: FamilyMember, itemId: number) => m.itemId === itemId && !m.finished && (m.positionMs > 0 || m.fraction > 0 || !!m.page)

/** A page reader's place: the page of a `page:N` locator (1-based), else from the fraction. */
export function pageOfMember(m: FamilyMember, pages: number): number {
  const n = m.page ?? Number(/^page:(\d+)$/.exec(m.locator ?? '')?.[1] ?? 0)
  if (n > 0) return Math.min(n, Math.max(pages, 1))
  return pages > 0 ? Math.min(pages, Math.max(1, Math.round(m.fraction * pages))) : 0
}

/** How long the jump target must actually play before it is this profile's position. */
export const HOLD_PLAY_SEC = 5

/** Where a jump was made from: until it plays on, the saved position stays `pos`. */
export interface Hold {
  /** The position before the jump (saved instead). */
  pos: number
  /** The jump target. */
  from: number
}

/**
 * The position to save while a jump is held: the one before it while the
 * player is still at the jump target (from − 1 … from + HOLD_PLAY_SEC), the
 * real one once it played on there or the viewer went elsewhere (released).
 * Units are the caller's (seconds or ms with `unit` 1000).
 */
export function heldPosition(hold: Hold | null, now: number, unit = 1): { pos: number; release: boolean } {
  if (!hold) return { pos: now, release: false }
  if (now >= hold.from - unit && now < hold.from + HOLD_PLAY_SEC * unit) return { pos: hold.pos, release: false }
  return { pos: now, release: true }
}

/** How long «Вернуться к …» stays offered after a jump. */
export const RETURN_MS = 10_000

export interface ReturnPoint<T> {
  /** Where «Вернуться» goes back to, and its label («12:40»). */
  to: T
  label: string
}

/**
 * «↩ Вернуться к 12:40»: offered for RETURN_MS after a jump, until used,
 * dismissed or another seek (`clear`).
 */
export function useReturnPoint<T>(ms = RETURN_MS) {
  const [back, setBack] = useState<ReturnPoint<T> | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const clear = useCallback(() => {
    if (timer.current) clearTimeout(timer.current)
    timer.current = null
    setBack(null)
  }, [])
  const offer = useCallback(
    (to: T, label: string) => {
      if (timer.current) clearTimeout(timer.current)
      setBack({ to, label })
      timer.current = setTimeout(() => {
        timer.current = null
        setBack(null)
      }, ms)
    },
    [ms],
  )
  useEffect(() => () => void (timer.current && clearTimeout(timer.current)), [])
  return { back, offer, clear }
}
