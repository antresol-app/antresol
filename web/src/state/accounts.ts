// The profile's linked accounts (GET /api/accounts), asked for once per
// session and kept: the write-back prompt after a mark change must not cost
// a request every time. null: the server has no accounts (library edition) or
// does not answer.
import { getAccounts } from '../api/client'
import type { AccountsState, MyShowsState, ShikimoriState } from '../api/types'
import { session } from './session'

let cache: Promise<AccountsState | null> | null = null
let cached: AccountsState | null = null // the resolved cache, for synchronous patches
let cacheUser: number | undefined

function load(): Promise<AccountsState | null> {
  const uid = session.get().user?.id
  if (uid !== cacheUser) cache = null // another profile signed in
  cacheUser = uid
  if (!cache) cached = null
  cache ??= getAccounts().then(
    (a) => {
      cached = a
      return a
    },
    () => {
      cache = null // try again next time
      return null
    },
  )
  return cache
}

export async function loadAccounts(): Promise<ShikimoriState | null> {
  return (await load())?.shikimori ?? null
}

/** The MyShows link; null when the server does not offer it (or has no accounts). */
export async function loadMyShows(): Promise<MyShowsState | null> {
  return (await load())?.myshows ?? null
}

// patch edits the cached state in place and synchronously, so two changes
// in a row cannot overwrite each other.
function patch(f: (a: AccountsState) => AccountsState) {
  const cur = cached
  if (!cur) return
  cached = f(cur)
  cache = Promise.resolve(cached)
  cacheUser = session.get().user?.id
}

/** What the profile just changed, so the cache does not go stale. */
export function setAccountsState(s: ShikimoriState) {
  patch((a) => ({ ...a, shikimori: s }))
}

export function setMyShowsState(m: MyShowsState) {
  patch((a) => ({ ...a, myshows: m }))
}

export function resetAccounts() {
  cache = null
  cached = null
}
