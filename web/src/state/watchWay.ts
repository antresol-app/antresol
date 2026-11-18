// The way of watching the owner last chose on a title, remembered per profile on
// this device: an id of a «Смотреть ▾» way, an addon's `<addonId>:<label>`.
// The bare `online` of before (the in-core way) reads as `online:way`, the
// online addon's way (wayId, the one mapping). Plain localStorage (any
// failure just means "not remembered"). An id no way on screen has any more
// reads as none there (pickWay). A tiny shared
// store: a choice on one card is at once the main action of every other one
// mounted (and of other tabs, through `storage` events).
import { useSyncExternalStore } from 'react'

/** A way's id: an addon way's `<addonId>:<label>`. */
export type WayId = string

/** The online way's id, the default way when nothing is remembered. */
export const ONLINE_WAY: WayId = 'online:way'

/** A way id as it reads now: the bare `online` of before is ONLINE_WAY; any other id is itself. */
export function wayId(id: string): WayId {
  return id === 'online' ? ONLINE_WAY : id
}

const PREFIX = 'antresol.watchWay.'
const key = (profileId: number) => `${PREFIX}${profileId}`

const listeners = new Set<() => void>()

export function readWatchWay(profileId: number): WayId | null {
  try {
    const v = localStorage.getItem(key(profileId))
    return v ? wayId(v) : null
  } catch {
    return null
  }
}

export function writeWatchWay(profileId: number, way: WayId): void {
  try {
    localStorage.setItem(key(profileId), way)
  } catch {
    /* private mode or blocked storage: the choice just isn't remembered */
  }
  for (const l of listeners) l()
}

function subscribe(onChange: () => void): () => void {
  listeners.add(onChange)
  // another tab chose (key null: its storage was cleared)
  const onStorage = (e: StorageEvent) => {
    if (e.key === null || e.key.startsWith(PREFIX)) onChange()
  }
  window.addEventListener('storage', onStorage)
  return () => {
    listeners.delete(onChange)
    window.removeEventListener('storage', onStorage)
  }
}

/** The remembered way of `profileId`, live: re-renders when any consumer (or another tab) chooses. */
export function useWatchWay(profileId: number): WayId | null {
  return useSyncExternalStore(
    subscribe,
    () => readWatchWay(profileId),
    () => null,
  )
}

/** The remembered way if it is still offered, else the online way, else the first offered. `available` must not be empty. */
export function pickWay(remembered: WayId | null, available: WayId[]): WayId {
  const r = remembered ? wayId(remembered) : null
  if (r && available.includes(r)) return r
  return available.includes(ONLINE_WAY) ? ONLINE_WAY : available[0]!
}
