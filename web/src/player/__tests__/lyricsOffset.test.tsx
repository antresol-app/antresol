import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { LyricsView, OFFSET_SAVE_MS } from '../LyricsView'

const LRC = '[00:01.00]Первая\n[00:03.00]Вторая\n[00:05.00]Третья'

type Call = { url: string; method: string; body?: unknown }
let calls: Call[]
let putStatus: number

function stubFetch(offsetMs?: number, synced = true) {
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? 'GET'
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
      calls.push({ url, method, body })
      if (method === 'PUT') {
        if (putStatus !== 200) return new Response(JSON.stringify({ error: 'Ошибка' }), { status: putStatus })
        return new Response(JSON.stringify({ offsetMs: (body as { offsetMs: number }).offsetMs }), { status: 200 })
      }
      return new Response(JSON.stringify({ lyrics: synced ? LRC : 'Просто текст', synced, offsetMs }), { status: 200 })
    }),
  )
}

const puts = () => calls.filter((c) => c.method === 'PUT')
const activeText = () => document.querySelector('[aria-current="true"]')?.textContent

beforeEach(() => {
  calls = []
  putStatus = 200
  vi.useFakeTimers({ shouldAdvanceTime: true })
})
afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('LyricsView offset', () => {
  it('applies the stored offset to the line times', async () => {
    stubFetch(1500)
    render(<LyricsView trackId={7} positionSec={3.2} onSeek={() => {}} canAdjust />)
    // 3,2 s with lines 1,5 s later: the first line (2,5 s) is current, not the second (4,5 s)
    await screen.findByText('Вторая')
    expect(activeText()).toBe('Первая')
    expect(screen.getByText('Текст: +1,50 с')).toBeInTheDocument()
  })

  it('«Раньше» / «Позже» move the lines at once and save once, debounced', async () => {
    stubFetch()
    const onSeek = vi.fn()
    render(<LyricsView trackId={7} positionSec={3.2} onSeek={onSeek} canAdjust />)
    await screen.findByText('Вторая')
    expect(activeText()).toBe('Вторая')
    expect(screen.queryByRole('button', { name: 'Сбросить сдвиг текста' })).not.toBeInTheDocument()
    const later = screen.getByRole('button', { name: 'Текст позже на 0,25 с' })
    fireEvent.click(later)
    expect(screen.getByText('Текст: +0,25 с')).toBeInTheDocument()
    expect(activeText()).toBe('Первая') // the second line is at 3,25 s now
    for (let i = 0; i < 3; i++) fireEvent.click(later)
    expect(screen.getByText('Текст: +1,00 с')).toBeInTheDocument()
    fireEvent.click(screen.getByText('Третья'))
    expect(onSeek).toHaveBeenCalledWith(6)
    expect(puts()).toHaveLength(0)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(OFFSET_SAVE_MS + 10)
    })
    expect(puts()).toEqual([{ url: '/api/music/tracks/7/lyrics/offset', method: 'PUT', body: { offsetMs: 1000 } }])
    fireEvent.click(screen.getByRole('button', { name: 'Текст раньше на 0,25 с' }))
    expect(screen.getByText('Текст: +0,75 с')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Сбросить сдвиг текста' }))
    expect(screen.queryByText(/Текст: /)).not.toBeInTheDocument()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(OFFSET_SAVE_MS + 10)
    })
    expect(puts().map((c) => c.body)).toEqual([{ offsetMs: 1000 }, { offsetMs: 0 }])
  })

  it('a failed save puts back the stored value', async () => {
    stubFetch(500)
    putStatus = 500
    render(<LyricsView trackId={7} positionSec={0} onSeek={() => {}} canAdjust />)
    await screen.findByText('Текст: +0,50 с')
    fireEvent.click(screen.getByRole('button', { name: 'Текст позже на 0,25 с' }))
    expect(screen.getByText('Текст: +0,75 с')).toBeInTheDocument()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(OFFSET_SAVE_MS + 10)
    })
    expect(puts()).toHaveLength(1)
    expect(await screen.findByText('Текст: +0,50 с')).toBeInTheDocument()
  })

  it('a change still waiting is saved when the view closes', async () => {
    stubFetch()
    const v = render(<LyricsView trackId={7} positionSec={0} onSeek={() => {}} canAdjust />)
    await screen.findByText('Вторая')
    fireEvent.click(screen.getByRole('button', { name: 'Текст раньше на 0,25 с' }))
    v.unmount()
    expect(puts().map((c) => c.body)).toEqual([{ offsetMs: -250 }])
  })

  it('a save answered after a skip leaves the next track alone', async () => {
    for (const fail of [true, false]) {
      calls = []
      let answer!: (r: Response) => void
      vi.stubGlobal(
        'fetch',
        vi.fn(async (url: string, init?: RequestInit) => {
          const method = init?.method ?? 'GET'
          calls.push({ url, method })
          if (method === 'PUT') return new Promise<Response>((r) => (answer = r))
          // track 7 is 0,50 s late; 8 has no offset
          return new Response(JSON.stringify({ lyrics: LRC, synced: true, offsetMs: url.includes('/7/') ? 500 : 0 }), { status: 200 })
        }),
      )
      const v = render(<LyricsView trackId={7} positionSec={0} onSeek={() => {}} canAdjust />)
      await screen.findByText('Текст: +0,50 с')
      fireEvent.click(screen.getByRole('button', { name: 'Текст позже на 0,25 с' }))
      await act(async () => {
        await vi.advanceTimersByTimeAsync(OFFSET_SAVE_MS + 10)
      })
      expect(puts()).toHaveLength(1)
      v.rerender(<LyricsView trackId={8} positionSec={0} onSeek={() => {}} canAdjust />)
      await screen.findByText('Вторая')
      expect(screen.queryByText(/Текст: /)).not.toBeInTheDocument()
      // the server clamps differently, or fails: either way 7's answer is not 8's
      await act(async () => {
        answer(fail ? new Response(JSON.stringify({ error: 'Ошибка' }), { status: 500 }) : new Response(JSON.stringify({ offsetMs: 1000 }), { status: 200 }))
        await vi.advanceTimersByTimeAsync(10)
      })
      expect(screen.queryByText(/Текст: /)).not.toBeInTheDocument()
      v.unmount()
    }
  })

  it('a stored offset off the step is shown as it is; the buttons step from it', async () => {
    stubFetch(1900)
    render(<LyricsView trackId={7} positionSec={0} onSeek={() => {}} canAdjust />)
    await screen.findByText('Текст: +1,90 с')
    fireEvent.click(screen.getByRole('button', { name: 'Текст раньше на 0,25 с' }))
    expect(screen.getByText('Текст: +1,65 с')).toBeInTheDocument()
  })

  it('no control for a kid profile or plain words', async () => {
    stubFetch(500)
    const v = render(<LyricsView trackId={7} positionSec={0} onSeek={() => {}} />)
    await screen.findByText('Вторая')
    expect(screen.queryByRole('group', { name: 'Синхронизация текста' })).not.toBeInTheDocument()
    v.unmount()
    stubFetch(0, false)
    render(<LyricsView trackId={8} positionSec={0} onSeek={() => {}} canAdjust />)
    await screen.findByText('Просто текст')
    expect(screen.queryByRole('group', { name: 'Синхронизация текста' })).not.toBeInTheDocument()
  })
})
