// «Общая медиатека / Моя медиатека»: which library the library pages list,
// as Photos' library picker («Обе медиатеки / Личная / Общая») does. The
// choice is the profile's, kept on the server (it follows the profile to
// every browser, as Photos syncs it through iCloud) and read from /api/me;
// a change shows at once and is saved behind the scenes. A kid's profile,
// the core build and a library-only server have no own titles to
// pick: there it is always the shared library and no control is shown.
import { useCallback } from 'react'
import { setLibraryScope as saveScope } from '../api/client'
import type { LibraryScope, User } from '../api/types'
import { session, useSession } from './session'

/** Whether this profile can switch to «Моя медиатека». */
export function canPickLibrary(user: User | null | undefined): user is User {
  return !!user && !user.isKid && !__LIBRARY_EDITION__ && !user.libraryOnly
}

/** The library the profile's pages list. */
export function libraryScopeOf(user: User | null | undefined): LibraryScope {
  return canPickLibrary(user) && user.libraryScope === 'mine' ? 'mine' : 'all'
}

// The latest pick: an answer to an older one changes nothing (all→mine→all
// in quick succession must end on «Общая» whatever order the saves finish in).
let picks = 0

/**
 * Shows `scope` at once and saves it for the profile; a failed save puts
 * the previous choice back (and rejects), unless a newer pick came since.
 * Only libraryScope is written, so a preference changed meanwhile stays.
 */
export async function pickLibraryScope(scope: LibraryScope): Promise<void> {
  const user = session.get().user
  if (!canPickLibrary(user) || libraryScopeOf(user) === scope) return
  const prev = user.libraryScope ?? 'all'
  const seq = ++picks
  const write = (libraryScope: LibraryScope) => {
    if (seq === picks) session.updateUser({ id: user.id, libraryScope })
  }
  write(scope)
  try {
    const me = await saveScope(scope)
    write(me.libraryScope ?? scope)
  } catch (e) {
    write(prev)
    throw e
  }
}

export interface LibraryScopeState {
  scope: LibraryScope
  /** The profile can switch (else the control is hidden and scope is 'all'). */
  available: boolean
  setScope: (scope: LibraryScope) => void
}

export function useLibraryScope(): LibraryScopeState {
  const { user } = useSession()
  const setScope = useCallback((scope: LibraryScope) => {
    pickLibraryScope(scope).catch((e: unknown) => console.warn('vault: library scope not saved', e))
  }, [])
  return { scope: libraryScopeOf(user), available: canPickLibrary(user), setScope }
}
