import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { clampSubtitleOffset, useSubtitleOffset } from '../useSubtitleOffset'

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('clampSubtitleOffset', () => {
  it('keeps the 0,25 s grid within ±60 s', () => {
    expect(clampSubtitleOffset(260)).toBe(250)
    expect(clampSubtitleOffset(-99_000)).toBe(-60_000)
    expect(clampSubtitleOffset(-0)).toBe(0)
  })
})

describe('useSubtitleOffset', () => {
  it('shows a press at once and saves the last one, debounced', async () => {
    const save = vi.fn(async (_f: number, ms: number) => ms)
    const { result } = renderHook(({ file, init }) => useSubtitleOffset(file, init, save), { initialProps: { file: 5, init: 500 } })
    expect(result.current[0]).toBe(500)
    act(() => result.current[1](750))
    act(() => result.current[1](1000))
    expect(result.current[0]).toBe(1000)
    expect(save).not.toHaveBeenCalled()
    await act(async () => vi.advanceTimersByTimeAsync(900))
    expect(save).toHaveBeenCalledTimes(1)
    expect(save).toHaveBeenCalledWith(5, 1000)
  })

  it('each part has its own; a change still waiting goes out when the part changes', async () => {
    const save = vi.fn(async (_f: number, ms: number) => ms)
    const { result, rerender } = renderHook(({ file, init }) => useSubtitleOffset(file, init, save), { initialProps: { file: 5, init: 0 } })
    act(() => result.current[1](-250))
    rerender({ file: 6, init: 1500 })
    expect(save).toHaveBeenCalledWith(5, -250)
    expect(result.current[0]).toBe(1500)
    rerender({ file: 5, init: 0 })
    expect(result.current[0]).toBe(-250)
  })

  it('a failed save puts back what the server had', async () => {
    const save = vi.fn(async () => Promise.reject(new Error('offline')))
    const { result } = renderHook(() => useSubtitleOffset(5, 250, save))
    act(() => result.current[1](500))
    await act(async () => vi.advanceTimersByTimeAsync(900))
    expect(result.current[0]).toBe(250)
  })

  it('without a save (a stream) it only lives in the player', async () => {
    const { result } = renderHook(() => useSubtitleOffset(0, 0))
    act(() => result.current[1](250))
    await act(async () => vi.advanceTimersByTimeAsync(900))
    expect(result.current[0]).toBe(250)
  })
})
