// «Общая медиатека / Моя медиатека»: which library the library pages list,
// as Photos' library picker («Обе медиатеки / Личная / Общая») does. The
// choice is the profile's, kept on the server (it follows the profile to
// every browser, as Photos syncs it through iCloud) and read from /api/me;
// a change shows at once and is saved behind the scenes. A kid's profile,
// and any profile without an addon that adds titles (useCanAdd), has no
// own titles to pick: there it is always the shared library and no control
// is shown.
import { useCallback } from 'react'
import { setLibraryScope as saveScope } from '../api/client'
import type { LibraryScope, User } from '../api/types'
import { useCanAddState } from '../addons/slots'
import { session, useSession } from './session'

/** Whether this profile can switch to «Моя медиатека» (`canAdd`: useCanAdd()). */
export function canPickLibrary(user: User | null | undefined, canAdd: boolean): user is User {
  return !!user && !user.isKid && canAdd
}

/** The library the profile's pages list. */
export function libraryScopeOf(user: User | null | undefined, canAdd: boolean): LibraryScope {
  return canPickLibrary(user, canAdd) && user.libraryScope === 'mine' ? 'mine' : 'all'
}

// The latest pick: an answer to an older one changes nothing (all→mine→all
// in quick succession must end on «Общая» whatever order the saves finish in).
let picks = 0

/**
 * Shows `scope` at once and saves it for the profile; a failed save puts
 * the previous choice back (and rejects), unless a newer pick came since.
 * Only libraryScope is written, so a preference changed meanwhile stays.
 */
export async function pickLibraryScope(scope: LibraryScope, canAdd: boolean): Promise<void> {
  const user = session.get().user
  if (!canPickLibrary(user, canAdd) || libraryScopeOf(user, canAdd) === scope) return
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
  // while the addons load, the profile's own choice stands (no first paint of the whole library)
  const known = useCanAddState()
  const canAdd = known === true
  const setScope = useCallback(
    (scope: LibraryScope) => {
      pickLibraryScope(scope, canAdd).catch((e: unknown) => console.warn('vault: library scope not saved', e))
    },
    [canAdd],
  )
  return { scope: libraryScopeOf(user, known ?? true), available: canPickLibrary(user, canAdd), setScope }
}
