// usePolling: asks on mount and every tick, pauses in a hidden tab, never overlaps, stops on unmount.
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { usePolling } from '../usePolling'

let hidden = false
const setHidden = (h: boolean) => {
  hidden = h
  document.dispatchEvent(new Event('visibilitychange'))
}

beforeEach(() => {
  vi.useFakeTimers()
  hidden = false
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden })
})
afterEach(() => {
  vi.useRealTimers()
  delete (document as unknown as Record<string, unknown>).hidden
})

describe('usePolling', () => {
  it('asks on mount and every tick', async () => {
    const task = vi.fn()
    renderHook(() => usePolling(task, 1000))
    expect(task).toHaveBeenCalledTimes(1)
    await act(() => vi.advanceTimersByTimeAsync(3000))
    expect(task).toHaveBeenCalledTimes(4)
  })

  it('does not ask on mount when told so', async () => {
    const task = vi.fn()
    renderHook(() => usePolling(task, 1000, { immediate: false }))
    expect(task).not.toHaveBeenCalled()
    await act(() => vi.advanceTimersByTimeAsync(1000))
    expect(task).toHaveBeenCalledTimes(1)
  })

  it('pauses in a hidden tab and asks once as it shows again', async () => {
    const task = vi.fn()
    renderHook(() => usePolling(task, 1000))
    await act(async () => setHidden(true))
    await act(() => vi.advanceTimersByTimeAsync(5000))
    expect(task).toHaveBeenCalledTimes(1)
    await act(async () => setHidden(false))
    expect(task).toHaveBeenCalledTimes(2)
  })

  it('keeps asking in a hidden tab when told so', async () => {
    const task = vi.fn()
    renderHook(() => usePolling(task, 1000, { runWhenHidden: true }))
    await act(async () => setHidden(true))
    await act(() => vi.advanceTimersByTimeAsync(2000))
    expect(task).toHaveBeenCalledTimes(3)
  })

  it('never overlaps: a slow answer skips the ticks it covers', async () => {
    let finish!: () => void
    const task = vi.fn(() => new Promise<void>((r) => (finish = r)))
    renderHook(() => usePolling(task, 1000))
    await act(() => vi.advanceTimersByTimeAsync(3000))
    expect(task).toHaveBeenCalledTimes(1)
    await act(async () => finish())
    await act(() => vi.advanceTimersByTimeAsync(1000))
    expect(task).toHaveBeenCalledTimes(2)
  })

  it('a failed task is asked again at the next tick', async () => {
    const task = vi.fn().mockRejectedValueOnce(new Error('down')).mockResolvedValue(undefined)
    renderHook(() => usePolling(task, 1000))
    await act(() => vi.advanceTimersByTimeAsync(1000))
    expect(task).toHaveBeenCalledTimes(2)
  })

  it('aborts and stops on unmount', async () => {
    const signals: AbortSignal[] = []
    const task = vi.fn((s: AbortSignal) => void signals.push(s))
    const { unmount } = renderHook(() => usePolling(task, 1000))
    unmount()
    expect(signals[0]!.aborted).toBe(true)
    await act(() => vi.advanceTimersByTimeAsync(5000))
    expect(task).toHaveBeenCalledTimes(1)
  })

  it('starts over when its key changes, with the latest task', async () => {
    const seen: number[] = []
    const { rerender } = renderHook(({ id }) => usePolling(() => void seen.push(id), 1000, { key: id }), { initialProps: { id: 1 } })
    rerender({ id: 2 })
    await act(() => vi.advanceTimersByTimeAsync(1000))
    expect(seen).toEqual([1, 2, 2])
  })

  it('a request that hangs is given up after three periods (at least 30 s): its signal is aborted and the next tick asks again', async () => {
    const signals: AbortSignal[] = []
    const task = vi.fn((s: AbortSignal) => {
      signals.push(s)
      return new Promise<void>(() => {}) // never answers, ignores the signal
    })
    renderHook(() => usePolling(task, 1000))
    await act(() => vi.advanceTimersByTimeAsync(29_000))
    expect(task).toHaveBeenCalledTimes(1)
    await act(() => vi.advanceTimersByTimeAsync(2000))
    expect(signals[0]!.aborted).toBe(true)
    expect(task.mock.calls.length).toBeGreaterThan(1)
    expect(signals[1]!.aborted).toBe(false)
  })

  it('a longer period gives a longer ceiling: three periods', async () => {
    const task = vi.fn(() => new Promise<void>(() => {}))
    renderHook(() => usePolling(task, 20_000))
    await act(() => vi.advanceTimersByTimeAsync(59_000))
    expect(task).toHaveBeenCalledTimes(1)
    await act(() => vi.advanceTimersByTimeAsync(2000))
    expect(task).toHaveBeenCalledTimes(2)
  })

  it('the tab showing again replaces a request still out', async () => {
    const signals: AbortSignal[] = []
    const task = vi.fn((s: AbortSignal) => {
      signals.push(s)
      return new Promise<void>(() => {})
    })
    renderHook(() => usePolling(task, 1000))
    await act(async () => setHidden(true))
    await act(async () => setHidden(false))
    expect(task).toHaveBeenCalledTimes(2)
    expect(signals[0]!.aborted).toBe(true)
    expect(signals[1]!.aborted).toBe(false)
  })
})
