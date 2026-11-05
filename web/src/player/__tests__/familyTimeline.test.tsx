import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, renderHook, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { FamilyMember, MediaFile } from '../../api/types'
import { session } from '../../state/session'
import { RETURN_MS, useReturnPoint } from '../familyMarks'

vi.mock('hls.js', () => {
  class FakeHls {
    static Events = { ERROR: 'error' }
    static ErrorTypes = { NETWORK_ERROR: 'networkError' }
    static isSupported = () => true
    on() {}
    loadSource() {}
    attachMedia() {}
    destroy() {}
  }
  return { default: FakeHls }
})

import { VideoPlayer } from '../VideoPlayer'

const MIN = 60_000
const adult = { id: 1, name: 'Александр', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }
const kid = { ...adult, id: 5, name: 'Вася', isAdmin: false, isKid: true }
const json = (b: unknown, status = 200) => new Response(b === undefined ? null : JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const member = (over: Partial<FamilyMember>): FamilyMember => ({
  userId: 2,
  name: 'Мама',
  avatar: 'grad-3',
  isKid: false,
  itemId: 9,
  positionMs: 0,
  durationMs: 60 * MIN,
  fraction: 0,
  finished: false,
  updatedAt: '2026-11-01T10:00:00Z',
  ...over,
})

let members: FamilyMember[] = []
let calls: { url: string; init?: RequestInit }[] = []

async function signIn(user: typeof adult) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      calls.push({ url, init })
      if (url.endsWith('/api/login')) return json(user)
      if (url.endsWith('/api/items/9/family')) return json({ members })
      if (url.includes('/api/progress/')) return json(undefined, 204)
      return json({})
    }),
  )
  await session.login(user.id)
}

function player(startSec = 760) {
  const start = vi.fn(async () => ({ mode: 'direct', url: '/api/stream/5', startSec: 0, durationMs: 60 * MIN, audioIndex: 0, audio: [], subtitles: [] }))
  render(<VideoPlayer itemId={9} title="Дюна" parts={[{ id: 5, name: 'dune.mkv', durationMs: 60 * MIN } as MediaFile]} startSec={startSec} onClose={() => {}} start={start as never} />)
  return start
}

const slider = () => screen.getByRole('slider', { name: 'Позиция' })
const saves = () => calls.filter((c) => c.url.includes('/api/progress/9') && c.init?.method === 'PUT').map((c) => JSON.parse(String(c.init!.body)) as { positionMs: number })

describe('VideoPlayer: «Семья» on the timeline', () => {
  beforeEach(() => {
    session.reset()
    localStorage.clear()
    calls = []
    members = []
    vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined)
    vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  })
  afterEach(() => vi.unstubAllGlobals())

  it('shows a face for everyone who is somewhere in this episode, and jumps there', async () => {
    members = [
      member({ positionMs: 34 * MIN + 12_000, fraction: 0.57 }),
      member({ userId: 3, name: 'Папа', avatar: 'grad-4', positionMs: 60 * MIN, finished: true }), // done: nothing to look at
      member({ userId: 4, name: 'Петя', itemId: 8, positionMs: 5 * MIN }), // another episode
    ]
    await signIn(adult)
    player()
    const group = await screen.findByRole('group', { name: 'Семья' })
    const faces = within(group).getAllByRole('button')
    expect(faces).toHaveLength(1)
    const mom = within(group).getByRole('button', { name: 'Перейти на место профиля «Мама» — 34:12' })
    expect(mom).toHaveTextContent('Мама · 34:12')
    expect(Number(mom.dataset.pct)).toBeCloseTo(57, 0)
    // after the slider in the tab order
    expect(slider().compareDocumentPosition(mom) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

    await userEvent.click(mom)
    expect(slider()).toHaveAttribute('aria-valuenow', String(34 * 60 + 12))
    const back = screen.getByRole('button', { name: 'Вернуться к 12:40' })

    // only looked at: a pause there saves the place before the jump
    fireEvent.pause(document.querySelector('video')!)
    await waitFor(() => expect(saves().at(-1)?.positionMs).toBe(760_000))

    await userEvent.click(back)
    expect(slider()).toHaveAttribute('aria-valuenow', '760')
    expect(screen.queryByRole('button', { name: /Вернуться к/ })).toBeNull()
  })

  it('jumps with the keyboard, and another seek takes the way back away', async () => {
    members = [member({ positionMs: 20 * MIN, fraction: 0.33 })]
    await signIn(adult)
    player()
    const mom = await screen.findByRole('button', { name: /место профиля «Мама»/ })
    mom.focus()
    await userEvent.keyboard('{Enter}')
    expect(slider()).toHaveAttribute('aria-valuenow', '1200')
    expect(screen.getByRole('button', { name: 'Вернуться к 12:40' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Вперёд на 10 секунд' }))
    expect(screen.queryByRole('button', { name: /Вернуться к/ })).toBeNull()
  })

  it('a dismissed way back is gone, the jump target stays', async () => {
    members = [member({ positionMs: 20 * MIN, fraction: 0.33 })]
    await signIn(adult)
    player()
    await userEvent.click(await screen.findByRole('button', { name: /место профиля «Мама»/ }))
    await userEvent.click(screen.getByRole('button', { name: 'Скрыть' }))
    expect(screen.queryByRole('button', { name: /Вернуться к/ })).toBeNull()
    expect(slider()).toHaveAttribute('aria-valuenow', '1200')
  })

  it('draws nothing when nobody else is in it', async () => {
    members = []
    await signIn(adult)
    player()
    await waitFor(() => expect(calls.some((c) => c.url.endsWith('/api/items/9/family'))).toBe(true))
    expect(screen.queryByRole('group', { name: 'Семья' })).toBeNull()
  })

  it('asks nothing for a kid', async () => {
    members = [member({ positionMs: 20 * MIN })]
    await signIn(kid)
    player()
    await act(() => new Promise((r) => setTimeout(r, 30)))
    expect(calls.some((c) => c.url.includes('/family'))).toBe(false)
    expect(screen.queryByRole('group', { name: 'Семья' })).toBeNull()
  })
})

describe('useReturnPoint', () => {
  afterEach(() => vi.useRealTimers())
  it('offers the way back for a while, then lets it go', () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useReturnPoint<number>())
    act(() => result.current.offer(760, '12:40'))
    expect(result.current.back).toEqual({ to: 760, label: '12:40' })
    act(() => vi.advanceTimersByTime(RETURN_MS - 100))
    expect(result.current.back).not.toBeNull()
    // a second jump starts the time again
    act(() => result.current.offer(760, '12:40'))
    act(() => vi.advanceTimersByTime(RETURN_MS - 100))
    expect(result.current.back).not.toBeNull()
    act(() => vi.advanceTimersByTime(200))
    expect(result.current.back).toBeNull()
  })
})
