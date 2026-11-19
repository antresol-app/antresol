import { useEffect, useLayoutEffect, useRef } from 'react'
import type { ServerEvent } from './types'
import { getLanguage, subscribeLanguage } from '../i18n'

type Handler = (e: ServerEvent) => void

// One EventSource per tab, shared by every subscriber.
let source: EventSource | null = null
const handlers = new Set<Handler>()
// onEvents' handlers that asked for 'open' too
const openers = new Set<Handler>()
// request.changed (a kid's request made or answered: {id, userId}) reaches the kid and the admins; disk.warning,
// migrate.progress and import.progress reach admins only. The addons' come as addon.<id>.<name> (listenEvent).
// addons.changed (an addon's status changed: {id, status}) reaches every adult profile.
const KNOWN = ['library.updated', 'request.changed', 'disk.warning', 'migrate.progress', 'import.progress', 'addons.changed']
// Event types added at runtime (addons: addon.<id>.<name>); kept across reconnects.
const extra = new Set<string>()

function listen(s: EventSource, t: string) {
  s.addEventListener(t, (m) => dispatch(t, (m as MessageEvent).data as string))
}

function dispatch(type: string, raw: string) {
  let data: unknown = raw
  try {
    data = JSON.parse(raw)
  } catch {
    /* plain text payload */
  }
  for (const h of handlers) h({ type, data })
}

function connect() {
  if (source || typeof EventSource === 'undefined') return
  // EventSource cannot send Accept-Language: the language goes in the query
  source = new EventSource(`/api/events?lang=${getLanguage()}`)
  source.onmessage = (m) => dispatch('message', m.data as string)
  // (re)connected: what was sent while it was away is lost, so «open» is said too
  source.onopen = () => {
    for (const h of openers) h({ type: 'open', data: '' })
  }
  for (const t of KNOWN) listen(source, t)
  for (const t of extra) listen(source, t)
  // EventSource reconnects by itself; errors never surface to the UI.
}

function disconnect() {
  source?.close()
  source = null
}

// A new interface language: reconnect so event texts come in it.
subscribeLanguage(() => {
  if (!source) return
  disconnect()
  connect()
})

/**
 * useEvents outside React (the addon loader): `handler` hears the events of
 * `types` (and 'open', each time the stream connects again, when asked for)
 * until the returned function is called.
 */
export function onEvents(handler: Handler, types: readonly string[]): () => void {
  const allow = new Set(types)
  const h: Handler = (e) => {
    if (allow.has(e.type)) handler(e)
  }
  handlers.add(h)
  if (allow.has('open')) openers.add(h)
  connect()
  return () => {
    handlers.delete(h)
    openers.delete(h)
    if (handlers.size === 0) disconnect()
  }
}

/** Delivers named events of `type` too (an SSE stream only hands over the names it is asked for). */
export function listenEvent(type: string) {
  if (KNOWN.includes(type) || extra.has(type)) return
  extra.add(type)
  if (source) listen(source, type)
}

/**
 * Subscribes to server-sent events. `types` filters by event type; omit for all.
 * The handler may change between renders without resubscribing.
 */
export function useEvents(handler: Handler, types?: readonly string[]) {
  const ref = useRef(handler)
  useLayoutEffect(() => {
    ref.current = handler
  })
  const key = types?.join(',') ?? ''
  useEffect(() => {
    const allow = key ? new Set(key.split(',')) : null
    const h: Handler = (e) => {
      if (!allow || allow.has(e.type)) ref.current(e)
    }
    handlers.add(h)
    connect()
    return () => {
      handlers.delete(h)
      if (handlers.size === 0) disconnect()
    }
  }, [key])
}
