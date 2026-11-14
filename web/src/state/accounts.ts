// The profile's linked accounts (GET /api/accounts), asked for once per
// session and kept: the write-back prompt after a mark change must not cost
// a request every time. null: the server has no accounts (library edition) or
// does not answer.
import { getAccounts } from '../api/client'
import type { ShikimoriState } from '../api/types'
import { session } from './session'

let cache: Promise<ShikimoriState | null> | null = null
let cacheUser: number | undefined

export function loadAccounts(): Promise<ShikimoriState | null> {
  const uid = session.get().user?.id
  if (uid !== cacheUser) cache = null // another profile signed in
  cacheUser = uid
  cache ??= getAccounts().then(
    (a) => a.shikimori,
    () => {
      cache = null // try again next time
      return null
    },
  )
  return cache
}

/** What the profile just changed, so the cache does not go stale. */
export function setAccountsState(s: ShikimoriState) {
  cache = Promise.resolve(s)
  cacheUser = session.get().user?.id
}

export function resetAccounts() {
  cache = null
}
