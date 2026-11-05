import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, renderHook, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { ScreenReport, ScreenStatus } from '../../api/types'
import { session } from '../../state/session'
import { resetScreenLimitsForTests, useScreenGuard, useScreenLimits, WARN_SEC } from '../../state/screenTime'
import { ScreenTimeChip } from '../ScreenTimeChip'
import { ScreenTimeSheet } from '../ScreenTimeSheet'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

const status = (over: Partial<ScreenStatus['video']> = {}): ScreenStatus => ({
  limited: true,
  day: '2026-11-02',
  hours: null,
  video: { limitMin: 60, bonusMin: 0, usedSec: 35 * 60, remainingSec: 25 * 60, allowed: true, ...over },
  audio: null,
  videoLimited: true,
  audioLimited: false,
})

type Call = { url: string; method: string; body?: unknown }
let calls: Call[]
let mine: ScreenStatus
// the status as the server counts it now (the remaining time running down)
let live: (() => ScreenStatus) | null
let report: ScreenReport

beforeEach(() => {
  calls = []
  mine = status()
  live = null
  report = {
    limits: { videoMin: 60, audioMin: null, weekday: { from: '08:00', to: '20:30' }, weekend: null, dayStart: '04:00' },
    status: status(),
    titles: [
      { itemId: 5, category: 'video', seconds: 1500, title: 'Смешарики', kind: 'series' },
      { itemId: 7, category: 'video', seconds: 600, title: 'Тачки', kind: 'movie' },
    ],
    days: ['10-27', '10-28', '10-29', '10-30', '10-31', '11-01', '11-02'].map((d, i) => ({ day: `2026-${d}`, videoSec: i * 600, audioSec: 0 })),
    passwordless: ['Мама', 'Петя'],
  }
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
      calls.push({ url, method, body })
      if (url.endsWith('/api/login')) return json({ id: 3, name: 'Вася', avatar: 'grad-1', hasPassword: false, isAdmin: false, isKid: true, maxRating: '6+' })
      if (url.endsWith('/api/me/screen-time')) return json(live ? live() : mine)
      if (url.endsWith('/screen-time/bonus')) {
        report = { ...report, status: status({ bonusMin: report.status.video.bonusMin + body.minutes }) }
        return json(report)
      }
      if (url.includes('/screen-time')) {
        if (method === 'PUT') report = { ...report, limits: body }
        return json(report)
      }
      return json({})
    }),
  )
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  session.reset()
})

describe('ScreenTimeChip', () => {
  it('shows a kid the time left today', async () => {
    await session.login(3)
    render(<ScreenTimeChip />)
    expect(await screen.findByRole('status', { name: 'Экранное время: сегодня можно ещё 25 мин' })).toBeInTheDocument()
  })

  it('shows the message when the time is over', async () => {
    mine = status({ allowed: false, remainingSec: 0, reason: 'limit', message: 'Время на сегодня закончилось. Завтра можно снова!' })
    await session.login(3)
    render(<ScreenTimeChip />)
    expect(await screen.findByText('Время на сегодня закончилось. Завтра можно снова!')).toBeInTheDocument()
  })

  it('shows nothing to a profile that is not limited', async () => {
    mine = { ...status(), limited: false }
    await session.login(3)
    const { container } = render(<ScreenTimeChip />)
    await waitFor(() => expect(calls.some((c) => c.url.endsWith('/api/me/screen-time'))).toBe(true))
    expect(container).toBeEmptyDOMElement()
  })
})

describe('useScreenGuard', () => {
  it('warns five minutes before the end and stops at it', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: false })
    const t0 = Date.now()
    live = () => status({ remainingSec: Math.max(0, WARN_SEC + 60 - Math.floor((Date.now() - t0) / 1000)) })
    const { result } = renderHook(() => useScreenGuard(true, 'Пока!'))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10)
    })
    expect(result.current.warnMinutes).toBeNull()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000)
    })
    expect(result.current.warnMinutes).toBe(5)
    expect(result.current.over).toBeNull()
    // the server says it is over
    live = null
    mine = status({ allowed: false, remainingSec: 0, reason: 'limit', message: 'Время на сегодня закончилось. Завтра можно снова!' })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(WARN_SEC * 1000 + 1000)
    })
    expect(result.current.over).toBe('Время на сегодня закончилось. Завтра можно снова!')
  })

  it('asks nothing when disabled', async () => {
    renderHook(() => useScreenGuard(false, 'Пока!'))
    await Promise.resolve()
    expect(calls.filter((c) => c.url.endsWith('/api/me/screen-time'))).toEqual([])
  })
})

describe('ScreenTimeSheet', () => {
  it('shows today by title, the week, gives a bonus and saves the limits', async () => {
    const user = userEvent.setup()
    const onClose = vi.fn()
    render(<ScreenTimeSheet userId={3} name="Вася" onClose={onClose} />)
    expect(await screen.findByText('Смешарики')).toBeInTheDocument()
    expect(screen.getByRole('note')).toHaveTextContent(
      'Ребёнок может переключиться на профили без пароля: Мама, Петя. Задайте им пароль, чтобы лимиты работали.',
    )
    expect(screen.getByText('35 мин из 1 ч')).toBeInTheDocument()
    expect(screen.getAllByRole('listitem').filter((li) => li.getAttribute('aria-label')?.includes(':'))).toHaveLength(7)

    await user.click(screen.getByRole('button', { name: 'Бонус на сегодня +30 мин' }))
    expect(calls.find((c) => c.url.endsWith('/api/users/3/screen-time/bonus'))?.body).toEqual({ minutes: 30 })
    expect(await screen.findByText('Бонус сегодня: +30 мин')).toBeInTheDocument()

    // an audio limit of 45 minutes; weekends without hours stay so
    await user.click(screen.getByRole('switch', { name: 'Музыка и аудиокниги в день: Ограничить' }))
    const minutes = screen.getAllByLabelText('Минут в день')
    await user.clear(minutes[1]!)
    await user.type(minutes[1]!, '45')
    await user.click(screen.getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(calls.find((c) => c.method === 'PUT')?.body).toEqual({
      videoMin: 60,
      audioMin: 45,
      weekday: { from: '08:00', to: '20:30' },
      weekend: null,
      dayStart: '04:00',
    })
  })
})

describe('useScreenLimits', () => {
  it('says which categories a kid may not download, asking once', async () => {
    resetScreenLimitsForTests()
    mine = { ...status(), videoLimited: false, audioLimited: true }
    await session.login(3)
    const a = renderHook(() => useScreenLimits(true))
    const b = renderHook(() => useScreenLimits(true))
    await waitFor(() => expect(a.result.current).toEqual({ video: false, audio: true }))
    await waitFor(() => expect(b.result.current).toEqual({ video: false, audio: true }))
    expect(calls.filter((c) => c.url.endsWith('/api/me/screen-time'))).toHaveLength(1)
    expect(renderHook(() => useScreenLimits(false)).result.current).toEqual({ video: false, audio: false })
  })
})
