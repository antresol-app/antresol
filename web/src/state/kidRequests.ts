// «Попросить»: a kid's own requests, by title key, and
// the admins' count of requests waiting for an answer. One copy per tab
// for the signed-in profile, loaded on first use and again on
// request.changed (asked, answered, approved by a download) and
// library.updated (a requested title may be in the library now).
import { useEffect, useSyncExternalStore } from 'react'
import { getMyRequests, getRequestCount, useEvents } from '../api/client'
import type { KidRequest } from '../api/types'

interface Store<T> {
  get: () => T
  subscribe: (f: () => void) => () => void
  /** Loads it for the profile (again when it is another profile's copy). */
  load: (profileId: number) => void
  /** Loads it again (requests that overlap share one). */
  refresh: () => void
  set: (v: T) => void
  reset: () => void
}

function makeStore<T>(empty: T, fetch: () => Promise<T>): Store<T> {
  let value = empty
  let owner = 0
  let inflight = false
  let again = false
  const subs = new Set<() => void>()
  const set = (v: T) => {
    value = v
    for (const f of subs) f()
  }
  const refresh = () => {
    if (!owner) return
    if (inflight) {
      again = true
      return
    }
    inflight = true
    const forOwner = owner
    fetch()
      .then((v) => forOwner === owner && set(v))
      .catch(() => {}) // a hint only: the last answer stays
      .finally(() => {
        inflight = false
        if (again) {
          again = false
          refresh()
        }
      })
  }
  return {
    get: () => value,
    subscribe: (f) => {
      subs.add(f)
      return () => subs.delete(f)
    },
    load: (profileId) => {
      // 0: a caller that has no use for it (its hook answers empty)
      if (!profileId || profileId === owner) return
      owner = profileId
      set(empty)
      refresh()
    },
    refresh,
    set,
    reset: () => {
      owner = 0
      value = empty
    },
  }
}

interface KidStores {
  none: ReadonlyMap<string, KidRequest>
  mine: Store<ReadonlyMap<string, KidRequest>>
  pending: Store<number>
}

// made on first use: a full-only module keeps no top-level calls, so the
// core build's bundle drops it (docs/build-tags.md)
let stores: KidStores | undefined
function kidStores(): KidStores {
  if (!stores) {
    const none: ReadonlyMap<string, KidRequest> = new Map()
    stores = {
      none,
      mine: makeStore(none, () => getMyRequests().then((list) => new Map(list.map((r) => [requestKey(r.kind, r.key), r])))),
      pending: makeStore(0, () => getRequestCount().then((c) => c.pending)),
    }
  }
  return stores
}

const RELOAD_ON = ['request.changed', 'library.updated'] as const

/** The key of a title in the kid's requests (kind and the server's key). */
export const requestKey = (kind: string, key: string) => `${kind}|${key}`

/**
 * The kid's requests by requestKey (profileId 0: none). `put` records the
 * answer of a new request at once.
 */
export function useMyRequests(profileId: number) {
  const { mine, none } = kidStores()
  useEffect(() => mine.load(profileId), [mine, profileId])
  useEvents(() => profileId && mine.refresh(), RELOAD_ON)
  const map = useSyncExternalStore(mine.subscribe, mine.get)
  return {
    requests: profileId ? map : none,
    put: (r: KidRequest) => mine.set(new Map(mine.get()).set(requestKey(r.kind, r.key), r)),
  }
}

/** Admins: how many requests wait for an answer (0 for anyone else). */
export function usePendingRequests(profileId: number, admin: boolean) {
  const { pending } = kidStores()
  const id = admin ? profileId : 0
  useEffect(() => pending.load(id), [pending, id])
  useEvents(() => id && pending.refresh(), RELOAD_ON)
  const n = useSyncExternalStore(pending.subscribe, pending.get)
  return id ? n : 0
}

/** The pending count again (after «Отказать» on this tab, before the event). */
export const refreshPendingRequests = () => kidStores().pending.refresh()

/** Forgets both (tests: the next profile loads its own even with the same id). */
export function resetKidRequests() {
  kidStores().mine.reset()
  kidStores().pending.reset()
}
