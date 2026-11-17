// The way of watching the owner last chose on a title, remembered per profile on
// this device: an id of a «Смотреть ▾» way (`online`, or an addon's
// `<addonId>:<label>`). Plain localStorage (any failure just means "not
// remembered"). An id no way on screen has any more reads as none there
// (pickWay). A tiny shared
// store: a choice on one card is at once the main action of every other one
// mounted (and of other tabs, through `storage` events).
import { useSyncExternalStore } from 'react'

/** A way's id: `online`, or an addon way's `<addonId>:<label>`. */
export type WayId = string

const PREFIX = 'antresol.watchWay.'
const key = (profileId: number) => `${PREFIX}${profileId}`

const listeners = new Set<() => void>()

export function readWatchWay(profileId: number): WayId | null {
  try {
    const v = localStorage.getItem(key(profileId))
    return v ? v : null
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

/** The remembered way if it is still offered, else online, else the first offered. `available` must not be empty. */
export function pickWay(remembered: WayId | null, available: WayId[]): WayId {
  if (remembered && available.includes(remembered)) return remembered
  return available.includes('online') ? 'online' : available[0]!
}
