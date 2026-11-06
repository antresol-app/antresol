// The shared loading hooks: usePagedList (pages, a refresh drops older
// answers), useLoad (load, quiet reload, update), usePresence (exit time).
import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

vi.mock('../../api/client', async (orig) => ({ ...(await orig<typeof import('../../api/client')>()), useEvents: () => undefined }))

const { usePagedList } = await import('../usePagedList')
const { useLoad } = await import('../useLoad')
const { usePresence } = await import('../../ui/presence')

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => ((resolve = res), (reject = rej)))
  return { promise, resolve, reject }
}

type Page = { items: string[]; total: number }

describe('usePagedList', () => {
  it('a page that comes back after a refresh started is dropped', async () => {
    const asks: { offset: number; d: ReturnType<typeof deferred<Page>> }[] = []
    const fetchPage = (offset: number) => {
      const d = deferred<Page>()
      asks.push({ offset, d })
      return d.promise
    }
    const { result } = renderHook(() => usePagedList<string>(fetchPage, [], { page: 2, failed: 'x', moreFailed: 'y' }))
    await act(async () => asks[0]!.d.resolve({ items: ['a', 'b'], total: 4 }))
    expect(result.current.state).toMatchObject({ status: 'ready', items: ['a', 'b'], more: true })
    act(() => result.current.loadMore()) // page 2 on its way
    act(() => result.current.load(true)) // the library changed: page 1 again
    await act(async () => asks[2]!.d.resolve({ items: ['n'], total: 1 }))
    await act(async () => asks[1]!.d.resolve({ items: ['c', 'd'], total: 4 })) // the old page 2, late
    expect(result.current.state).toMatchObject({ status: 'ready', items: ['n'], more: false })
  })

  it('pages by items, and says when there is no more', async () => {
    const fetchPage = vi.fn(async (offset: number) => ({ items: offset === 0 ? ['a', 'b'] : ['c'], total: 3 }))
    const { result } = renderHook(() => usePagedList<string>(fetchPage, [], { page: 2, failed: 'x', moreFailed: 'y' }))
    await waitFor(() => expect(result.current.state.status).toBe('ready'))
    await act(async () => result.current.loadMore())
    expect(fetchPage).toHaveBeenLastCalledWith(2, 2, expect.any(AbortSignal))
    expect(result.current.state).toMatchObject({ items: ['a', 'b', 'c'], more: false })
  })
})

describe('useLoad', () => {
  it('loads, says why it failed, reloads quietly and updates in place', async () => {
    let n = 0
    const fetch = vi.fn(async () => {
      n++
      if (n === 1) throw new Error('down')
      return { likes: n }
    })
    const { result } = renderHook(() => useLoad(fetch, [], { message: () => 'Не удалось' }))
    await waitFor(() => expect(result.current.state).toEqual({ status: 'error', message: 'Не удалось' }))
    act(() => result.current.load())
    await waitFor(() => expect(result.current.state).toEqual({ status: 'ready', data: { likes: 2 } }))
    act(() => result.current.update((d) => ({ likes: d.likes + 10 })))
    expect(result.current.state).toEqual({ status: 'ready', data: { likes: 12 } })
    act(() => result.current.load(true))
    // quiet: what is shown stays until the new answer is in
    expect(result.current.state.status).toBe('ready')
    await waitFor(() => expect(result.current.state).toEqual({ status: 'ready', data: { likes: 3 } }))
  })
})

describe('usePresence', () => {
  afterEach(() => vi.useRealTimers())

  it('stays mounted, leaving, for the exit time after it closes', () => {
    vi.useFakeTimers()
    const { result, rerender } = renderHook(({ open }) => usePresence(open, 240), { initialProps: { open: true } })
    expect(result.current).toEqual({ mounted: true, leaving: false })
    rerender({ open: false })
    expect(result.current).toEqual({ mounted: true, leaving: true })
    act(() => vi.advanceTimersByTime(240))
    expect(result.current).toEqual({ mounted: false, leaving: false })
  })
})
