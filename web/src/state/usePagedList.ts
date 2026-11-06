// A list the server sends in pages (the library's grids, «Песни»): the first
// page, then the next ones as the end comes near (an IntersectionObserver on
// a sentinel under the list), reloaded quietly on `library.updated`. One
// implementation for Browse, Albums, Artists, BooksGrid and Songs.
import { useCallback, useEffect, useRef, useState, type RefObject } from 'react'
import { errorText, useEvents } from '../api/client'

export interface Page<T> {
  items: T[]
  total: number
}

export type PagedState<T> = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; items: T[]; total: number; more: boolean }

export interface PagedListOptions {
  /** Items per page. */
  page: number
  /** The message when the first page cannot be loaded (an ApiError's own message wins). */
  failed: string
  /** The message when a later page cannot be loaded. */
  moreFailed: string
  /**
   * How the next offset counts: `items` (what came back so far) or `page`
   * (one page further: the server pages over entries this profile cannot see).
   */
  step?: 'items' | 'page'
}

export interface PagedList<T> {
  state: PagedState<T>
  /** Loads the first page again (`quiet`: keeps what is shown until it is in). */
  load: (quiet?: boolean) => void
  /** Loads the next page (the sentinel does it; also the retry button). */
  loadMore: () => void
  /** Why the last «more» failed ('' when it did not). */
  moreError: string
  /** Put under the list, while there is more: coming near it loads the next page. */
  sentinel: RefObject<HTMLDivElement | null>
}

/** `fetchPage(offset, limit, signal)`; `deps` are what the list depends on (sort, scope, kind). */
export function usePagedList<T>(
  fetchPage: (offset: number, limit: number, signal: AbortSignal) => Promise<Page<T>>,
  deps: unknown[],
  opts: PagedListOptions,
): PagedList<T> {
  const { page, step = 'items' } = opts
  const [state, setState] = useState<PagedState<T>>({ status: 'loading' })
  const [moreError, setMoreError] = useState('')
  const abortRef = useRef<AbortController | null>(null)
  const loadingMore = useRef(false)
  const next = useRef(0)
  const more = useRef(false)
  // bumped by every first-page load: a page that comes back for an older one is dropped
  const generation = useRef(0)
  const sentinel = useRef<HTMLDivElement>(null)
  const latest = useRef({ fetchPage, opts })
  latest.current = { fetchPage, opts }

  const load = useCallback((quiet = false) => {
    abortRef.current?.abort()
    const ac = new AbortController()
    abortRef.current = ac
    const gen = ++generation.current
    loadingMore.current = false
    if (!quiet) setState({ status: 'loading' })
    latest.current
      .fetchPage(0, page, ac.signal)
      .then((p) => {
        if (gen !== generation.current) return
        next.current = step === 'page' ? page : p.items.length
        more.current = next.current < p.total
        setMoreError('')
        setState({ status: 'ready', items: p.items, total: p.total, more: more.current })
      })
      .catch((e: unknown) => {
        if (ac.signal.aborted || quiet || gen !== generation.current) return
        setState({ status: 'error', message: errorText(e, latest.current.opts.failed) })
      })
    // the list's inputs are the caller's deps
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  useEffect(() => {
    load()
    return () => abortRef.current?.abort()
  }, [load])

  useEvents(() => load(true), ['library.updated'])

  const loadMore = useCallback(() => {
    if (!more.current || loadingMore.current) return
    loadingMore.current = true
    setMoreError('')
    const gen = generation.current
    const ac = abortRef.current ?? new AbortController()
    latest.current
      .fetchPage(next.current, page, ac.signal)
      .then((p) => {
        if (gen !== generation.current) return
        next.current = step === 'page' ? next.current + page : next.current + p.items.length
        more.current = p.items.length > 0 || step === 'page' ? next.current < p.total : false
        setState((s) => (s.status === 'ready' ? { status: 'ready', items: [...s.items, ...p.items], total: p.total, more: more.current } : s))
      })
      .catch((e: unknown) => {
        if (ac.signal.aborted || gen !== generation.current) return
        setMoreError(errorText(e, latest.current.opts.moreFailed))
      })
      .finally(() => {
        if (gen === generation.current) loadingMore.current = false
      })
  }, [page, step])

  // the sentinel comes near the viewport: the next page
  const ready = state.status === 'ready'
  const count = ready ? state.items.length : 0
  useEffect(() => {
    const el = sentinel.current
    if (!el || !ready) return
    const io = new IntersectionObserver((es) => es.some((e) => e.isIntersecting) && loadMore(), { rootMargin: '800px' })
    io.observe(el)
    return () => io.disconnect()
  }, [loadMore, ready, count, moreError])

  return { state, load, loadMore, moreError, sentinel }
}
