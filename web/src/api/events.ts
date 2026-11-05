import { useEffect, useLayoutEffect, useRef } from 'react'
import type { ServerEvent } from './types'
import { getLanguage, subscribeLanguage } from '../i18n'

type Handler = (e: ServerEvent) => void

// One EventSource per tab, shared by every subscriber.
let source: EventSource | null = null
const handlers = new Set<Handler>()
// request.changed (a kid's request made or answered: {id, userId}) reaches the kid and the admins; disk.warning,
// migrate.progress and import.progress reach admins only. Another build adds events of its own.
const CORE_EVENTS = ['library.updated', 'request.changed', 'disk.warning', 'migrate.progress', 'import.progress']
const KNOWN = __LIBRARY_EDITION__ ? CORE_EVENTS : [...CORE_EVENTS, 'torrent.progress', 'source.status', 'follow.queued', 'follow.checked']

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
  for (const t of KNOWN) source.addEventListener(t, (m) => dispatch(t, (m as MessageEvent).data as string))
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
