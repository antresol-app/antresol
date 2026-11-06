// The one way a page loads what it shows: a request that is aborted when the
// page leaves or asks again, a loading → ready | error state, a quiet reload
// on the server's events (the shown data stays until the new one is in), and
// an update for what the page changes itself (a «Нравится»).
import { useCallback, useEffect, useRef, useState } from 'react'
import { useEvents } from '../api/client'

export type Loaded<T> = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; data: T }

export interface LoadOptions {
  /** What to say when it fails (errorText(e, …) for the server's own words). */
  message: (e: unknown) => string
  /** The server's events that reload it quietly (default library.updated; [] for none). */
  events?: string[]
}

export interface LoadResult<T> {
  state: Loaded<T>
  /** Loads again (`quiet`: keeps what is shown, and an error does not replace it). */
  load: (quiet?: boolean) => void
  /** Changes the shown data in place. */
  update: (fn: (data: T) => T) => void
}

const LIBRARY = ['library.updated']

/** `fetch(signal)`; `deps` are what it depends on (the id). */
export function useLoad<T>(fetch: (signal: AbortSignal) => Promise<T>, deps: unknown[], opts: LoadOptions): LoadResult<T> {
  const [state, setState] = useState<Loaded<T>>({ status: 'loading' })
  const abortRef = useRef<AbortController | null>(null)
  const latest = useRef({ fetch, opts })
  latest.current = { fetch, opts }

  const load = useCallback((quiet = false) => {
    abortRef.current?.abort()
    const ac = new AbortController()
    abortRef.current = ac
    if (!quiet) setState({ status: 'loading' })
    latest.current
      .fetch(ac.signal)
      .then((data) => setState({ status: 'ready', data }))
      .catch((e: unknown) => {
        if (ac.signal.aborted || quiet) return
        setState({ status: 'error', message: latest.current.opts.message(e) })
      })
    // what it loads is the caller's deps
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps)

  useEffect(() => {
    load()
    return () => abortRef.current?.abort()
  }, [load])

  useEvents(() => load(true), opts.events ?? LIBRARY)

  const update = useCallback((fn: (data: T) => T) => setState((s) => (s.status === 'ready' ? { status: 'ready', data: fn(s.data) } : s)), [])

  return { state, load, update }
}
