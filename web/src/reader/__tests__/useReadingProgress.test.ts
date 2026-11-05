import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'

const saveReading = vi.fn((..._a: unknown[]) => Promise.resolve())
vi.mock('../../api/client', async (orig) => ({
  ...(await orig<typeof import('../../api/client')>()),
  saveReading: (...a: unknown[]) => saveReading(...a),
}))

import { useReadingProgress, type ReadingPosition } from '../useReadingProgress'

beforeEach(() => {
  vi.useFakeTimers()
  saveReading.mockClear()
})
afterEach(() => vi.useRealTimers())

describe('useReadingProgress', () => {
  it('debounces and flushes on pagehide with keepalive', () => {
    const { rerender, unmount } = renderHook(({ pos }: { pos: ReadingPosition | null }) => useReadingProgress(7, pos), {
      initialProps: { pos: null as ReadingPosition | null },
    })
    rerender({ pos: { locator: 'epubcfi(/6/2!/4/2)', fraction: 0.01 } })
    vi.advanceTimersByTime(1000)
    rerender({ pos: { locator: 'epubcfi(/6/4!/4/2)', fraction: 0.1 } })
    vi.advanceTimersByTime(1999)
    expect(saveReading).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1)
    expect(saveReading).toHaveBeenCalledTimes(1)
    expect(saveReading).toHaveBeenLastCalledWith(7, 'epubcfi(/6/4!/4/2)', 0.1, false)

    // nothing new: no save on pagehide
    window.dispatchEvent(new Event('pagehide'))
    expect(saveReading).toHaveBeenCalledTimes(1)

    rerender({ pos: { locator: 'epubcfi(/6/6!/4/2)', fraction: 0.34 } })
    window.dispatchEvent(new Event('pagehide'))
    expect(saveReading).toHaveBeenCalledTimes(2)
    expect(saveReading).toHaveBeenLastCalledWith(7, 'epubcfi(/6/6!/4/2)', 0.34, true)
    // the debounce timer was cancelled by the flush
    vi.advanceTimersByTime(5000)
    expect(saveReading).toHaveBeenCalledTimes(2)

    rerender({ pos: { locator: 'epubcfi(/6/8!/4/2)', fraction: 0.4 } })
    unmount()
    expect(saveReading).toHaveBeenCalledTimes(3)
    expect(saveReading).toHaveBeenLastCalledWith(7, 'epubcfi(/6/8!/4/2)', 0.4, true)
  })

  it('saves on visibilitychange to hidden', () => {
    const { rerender } = renderHook(({ pos }: { pos: ReadingPosition | null }) => useReadingProgress(3, pos), {
      initialProps: { pos: { locator: 'page:2', fraction: 0.5 } as ReadingPosition | null },
    })
    rerender({ pos: { locator: 'page:3', fraction: 0.75 } })
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' })
    document.dispatchEvent(new Event('visibilitychange'))
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' })
    expect(saveReading).toHaveBeenLastCalledWith(3, 'page:3', 0.75, true)
  })
})

describe('useReadingProgress, round 1', () => {
  it('does not save the position the book was opened at', () => {
    const initial = { locator: 'epubcfi(/6/4!/4/2)', fraction: 0.3 }
    const { rerender, unmount } = renderHook(({ pos }: { pos: ReadingPosition | null }) => useReadingProgress(9, pos, initial), {
      initialProps: { pos: initial as ReadingPosition | null },
    })
    vi.advanceTimersByTime(5000)
    window.dispatchEvent(new Event('pagehide'))
    expect(saveReading).not.toHaveBeenCalled()
    rerender({ pos: { locator: 'epubcfi(/6/6!/4/2)', fraction: 0.4 } })
    unmount()
    expect(saveReading).toHaveBeenCalledWith(9, 'epubcfi(/6/6!/4/2)', 0.4, true)
  })

  it('reports failed saves after two attempts and clears on success', async () => {
    const { ApiError } = await import('../../api/client')
    saveReading.mockImplementation(() => Promise.reject(new ApiError(500, 'x')))
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
    const { result, rerender } = renderHook(({ pos }: { pos: ReadingPosition | null }) => useReadingProgress(4, pos), {
      initialProps: { pos: { locator: 'page:2', fraction: 0.1 } as ReadingPosition | null },
    })
    await act(() => vi.advanceTimersByTimeAsync(2000))
    expect(result.current.error).toBeNull()
    rerender({ pos: { locator: 'page:3', fraction: 0.2 } })
    await act(() => vi.advanceTimersByTimeAsync(2000))
    expect(result.current.error).toBe('Позиция не сохранена')
    expect(warn).toHaveBeenCalled()
    saveReading.mockImplementation(() => Promise.reject(new ApiError(0, 'offline')))
    rerender({ pos: { locator: 'page:4', fraction: 0.3 } })
    await act(() => vi.advanceTimersByTimeAsync(2000))
    expect(result.current.error).toBe('Позиция не сохранена — нет связи')
    saveReading.mockImplementation(() => Promise.resolve())
    rerender({ pos: { locator: 'page:5', fraction: 0.4 } })
    await act(() => vi.advanceTimersByTimeAsync(2000))
    expect(result.current.error).toBeNull()
  })

  it('saves nothing while a jump to another profile’s place is held, and the page read on to after it', () => {
    const { result, rerender, unmount } = renderHook(({ pos }: { pos: ReadingPosition | null }) => useReadingProgress(4, pos), {
      initialProps: { pos: { locator: 'page:12', fraction: 0.3 } as ReadingPosition | null },
    })
    vi.advanceTimersByTime(2000)
    expect(saveReading).toHaveBeenLastCalledWith(4, 'page:12', 0.3, false)
    saveReading.mockClear()

    // Мама's page 30: only looked at
    act(() => result.current.hold(true))
    rerender({ pos: { locator: 'page:30', fraction: 0.75 } })
    vi.advanceTimersByTime(5000)
    window.dispatchEvent(new Event('pagehide'))
    expect(saveReading).not.toHaveBeenCalled()

    // the reader turns the page there: from now on it is where this profile reads
    act(() => result.current.hold(false))
    rerender({ pos: { locator: 'page:31', fraction: 0.775 } })
    vi.advanceTimersByTime(2000)
    expect(saveReading).toHaveBeenCalledTimes(1)
    expect(saveReading).toHaveBeenLastCalledWith(4, 'page:31', 0.775, false)

    // held again and closed there: the old place stays
    act(() => result.current.hold(true))
    rerender({ pos: { locator: 'page:2', fraction: 0.05 } })
    unmount()
    expect(saveReading).toHaveBeenCalledTimes(1)
  })
})
