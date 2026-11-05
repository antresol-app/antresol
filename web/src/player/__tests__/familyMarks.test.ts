import { describe, expect, it } from 'vitest'
import type { FamilyMember } from '../../api/types'
import { hasPlace, heldPosition, HOLD_PLAY_SEC, pageOfMember, placeMarks, type FamilyPlace } from '../familyMarks'

const member = (over: Partial<FamilyMember> = {}): FamilyMember => ({
  userId: 2,
  name: 'Мама',
  avatar: 'grad-3',
  isKid: false,
  itemId: 10,
  positionMs: 0,
  durationMs: 0,
  fraction: 0,
  finished: false,
  updatedAt: '2026-11-01T10:00:00Z',
  ...over,
})
const place = (userId: number, at: number): FamilyPlace => ({ member: member({ userId, name: `P${userId}` }), at, label: String(at) })

describe('placeMarks', () => {
  it('puts each place at its share of the timeline', () => {
    const m = placeMarks([place(2, 30), place(3, 90)], 120, 600)
    expect(m.map((x) => [x.member.userId, x.pct, x.offset])).toEqual([
      [2, 25, 0],
      [3, 75, 0],
    ])
  })

  it('clamps places before the start or past the end to the track', () => {
    const m = placeMarks([place(2, -5), place(3, 500)], 100, 600)
    expect(m.map((x) => x.pct)).toEqual([0, 100])
  })

  it('draws nothing without a length or places, and skips a place that is not a number', () => {
    expect(placeMarks([place(2, 10)], 0, 600)).toEqual([])
    expect(placeMarks([], 100, 600)).toEqual([])
    expect(placeMarks([place(2, NaN), place(3, 10)], 100, 600).map((x) => x.member.userId)).toEqual([3])
  })

  it('fans out faces closer than a face apart around their middle', () => {
    // 600 px track, 20 px face: closer than 3.33 % is one cluster
    const m = placeMarks([place(4, 51), place(2, 50), place(3, 52), place(5, 80)], 100, 600)
    const cluster = m.filter((x) => x.member.userId !== 5)
    expect(cluster.map((x) => x.member.userId)).toEqual([2, 4, 3])
    expect(new Set(cluster.map((x) => x.pct))).toEqual(new Set([51]))
    expect(cluster.map((x) => x.offset)).toEqual([-15, 0, 15])
    expect(m.find((x) => x.member.userId === 5)).toMatchObject({ pct: 80, offset: 0 })
  })

  it('keeps faces apart on a wide track that sit together on a narrow one', () => {
    const places = [place(2, 50), place(3, 52)]
    expect(placeMarks(places, 100, 2000).map((x) => x.offset)).toEqual([0, 0])
    expect(placeMarks(places, 100, 300).map((x) => x.offset)).toEqual([-7.5, 7.5])
  })
})

describe('hasPlace', () => {
  it('is a started, unfinished place in this very item', () => {
    expect(hasPlace(member({ positionMs: 60_000 }), 10)).toBe(true)
    expect(hasPlace(member({ fraction: 0.4, locator: 'epubcfi(/6/4)' }), 10)).toBe(true)
    expect(hasPlace(member({ positionMs: 60_000 }), 11)).toBe(false) // another episode
    expect(hasPlace(member({ positionMs: 60_000, finished: true }), 10)).toBe(false)
    expect(hasPlace(member(), 10)).toBe(false)
  })
})

describe('pageOfMember', () => {
  it('reads the page of a page:N place, else from the fraction, within the pages', () => {
    expect(pageOfMember(member({ page: 12 }), 40)).toBe(12)
    expect(pageOfMember(member({ locator: 'page:7' }), 40)).toBe(7)
    expect(pageOfMember(member({ page: 90 }), 40)).toBe(40)
    expect(pageOfMember(member({ fraction: 0.5 }), 40)).toBe(20)
    expect(pageOfMember(member({ fraction: 0 }), 40)).toBe(1)
  })
})

describe('heldPosition', () => {
  const hold = { pos: 760, from: 2052 }
  it('saves the position before the jump while the player is still at the jump target', () => {
    expect(heldPosition(hold, 2052)).toEqual({ pos: 760, release: false })
    expect(heldPosition(hold, 2052 + HOLD_PLAY_SEC - 0.5)).toEqual({ pos: 760, release: false })
  })
  it('lets go once it played on there, or the viewer went elsewhere', () => {
    expect(heldPosition(hold, 2052 + HOLD_PLAY_SEC)).toEqual({ pos: 2052 + HOLD_PLAY_SEC, release: true })
    expect(heldPosition(hold, 100)).toEqual({ pos: 100, release: true })
    expect(heldPosition(null, 100)).toEqual({ pos: 100, release: false })
  })
  it('works in milliseconds', () => {
    expect(heldPosition({ pos: 1000, from: 60_000 }, 62_000, 1000)).toEqual({ pos: 1000, release: false })
    expect(heldPosition({ pos: 1000, from: 60_000 }, 66_000, 1000)).toEqual({ pos: 66_000, release: true })
  })
})
