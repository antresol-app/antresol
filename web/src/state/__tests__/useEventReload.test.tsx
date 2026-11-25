// useEventReload: one reload for a burst of events, none in a hidden tab (once it shows again).
import { act, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

let emit: () => void = () => undefined
vi.mock('../../api/client', async (orig) => ({
  ...(await orig<typeof import('../../api/client')>()),
  useEvents: (h: () => void) => {
    emit = h
  },
}))

const { useEventReload } = await import('../useEventReload')

let hidden = false
beforeEach(() => {
  vi.useFakeTimers()
  hidden = false
  Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden })
})
afterEach(() => {
  vi.useRealTimers()
  delete (document as unknown as Record<string, unknown>).hidden
})

describe('useEventReload', () => {
  it('answers a burst of events with one reload', () => {
    const reload = vi.fn()
    renderHook(() => useEventReload(reload, ['library.updated'], 400))
    emit()
    act(() => vi.advanceTimersByTime(100))
    emit()
    emit()
    expect(reload).not.toHaveBeenCalled()
    act(() => vi.advanceTimersByTime(300))
    expect(reload).toHaveBeenCalledTimes(1)
    emit() // a later event is a new burst
    act(() => vi.advanceTimersByTime(400))
    expect(reload).toHaveBeenCalledTimes(2)
  })

  it('waits in a hidden tab and reloads once as it shows again', () => {
    const reload = vi.fn()
    renderHook(() => useEventReload(reload, ['library.updated']))
    hidden = true
    emit()
    emit()
    act(() => vi.advanceTimersByTime(1000))
    expect(reload).not.toHaveBeenCalled()
    hidden = false
    act(() => void document.dispatchEvent(new Event('visibilitychange')))
    expect(reload).toHaveBeenCalledTimes(1)
    act(() => void document.dispatchEvent(new Event('visibilitychange')))
    expect(reload).toHaveBeenCalledTimes(1)
  })

  it('drops a pending reload on unmount', () => {
    const reload = vi.fn()
    const { unmount } = renderHook(() => useEventReload(reload, ['library.updated']))
    emit()
    unmount()
    act(() => vi.advanceTimersByTime(1000))
    expect(reload).not.toHaveBeenCalled()
  })
})
