// Session state: who is signed in on this device. A tiny external store so it can
// be read from React (useSession) and from plain code/tests (session.get()).
import { useSyncExternalStore } from 'react'
import * as api from '../api/client'
import type { User } from '../api/types'
import { t } from '../i18n'

export type SessionStatus = 'loading' | 'setup' | 'signed-out' | 'signed-in' | 'offline'

export interface SessionState {
  status: SessionStatus
  user: User | null
  /** Outside the home network (from the server): profiles without a password cannot sign in. */
  remote: boolean
}

/** Shown when a profile without a password is picked outside the home network. */
export const remotePasswordless = () => t('app.session.remotePasswordless')

const initial: SessionState = { status: 'loading', user: null, remote: false }
let state: SessionState = initial
const listeners = new Set<() => void>()

/**
 * Runs before a new state is published, with the state still current (so
 * code that asks «who is signed in» still gets the outgoing profile).
 * `canSend`: the outgoing profile's cookie still works, so a last save can
 * go to the server under it; a returned promise is awaited (briefly) before
 * signing out. Otherwise the cookie is gone or already another profile's.
 */
export type BeforeChange = (next: SessionState, prev: SessionState, canSend: boolean) => void | Promise<unknown>
const guards = new Set<BeforeChange>()
/** How long a sign-out waits for the last saves under the old cookie. */
const FLUSH_MS = 3000

function runGuards(next: SessionState, canSend: boolean): Promise<unknown>[] {
  const waits: Promise<unknown>[] = []
  for (const g of guards) {
    try {
      const r = g(next, state, canSend)
      if (r) waits.push(r)
    } catch (e) {
      console.warn('vault: session change guard failed', e)
    }
  }
  return waits
}

function set(next: SessionState) {
  runGuards(next, false)
  state = next
  for (const l of listeners) l()
}

function signedIn(user: User) {
  set({ status: 'signed-in', user, remote: user.remote ?? false })
  return user
}

export const session = {
  get: () => state,
  subscribe(l: () => void) {
    listeners.add(l)
    return () => {
      listeners.delete(l)
    }
  },
  /** Called before every change is published (see BeforeChange). */
  beforeChange(g: BeforeChange) {
    guards.add(g)
    return () => {
      guards.delete(g)
    }
  },
  /** Newer fields of the signed-in profile (a preference saved; a whole copy or only the changed fields): ignored for another profile. */
  updateUser(user: Pick<User, 'id'> & Partial<User>) {
    if (state.status !== 'signed-in' || state.user?.id !== user.id) return
    set({ ...state, user: { ...state.user, ...user } })
  },
  /** For tests. */
  reset() {
    set(initial)
  },

  /** Works out where the app should start: first-run setup, profile picker or signed in. */
  async bootstrap(): Promise<void> {
    try {
      const me = await api.getMe()
      signedIn(me)
      return
    } catch (e) {
      if (!(e instanceof api.ApiError) || e.status === 0) {
        set({ status: 'offline', user: null, remote: state.remote })
        return
      }
    }
    try {
      const { needsSetup, remote } = await api.getSetup()
      set({ status: needsSetup ? 'setup' : 'signed-out', user: null, remote: remote ?? false })
    } catch {
      set({ status: 'offline', user: null, remote: state.remote })
    }
  },

  async setup(name: string, password: string) {
    return signedIn(await api.setup(name, password))
  },

  async login(userId: number, password?: string) {
    return signedIn(await api.login(userId, password))
  },

  /** Signs out on this device and returns to the profile picker. */
  async logout() {
    const next: SessionState = { status: 'signed-out', user: null, remote: state.remote }
    // the last saves go out while the cookie is still this profile's
    const waits = runGuards(next, true)
    if (waits.length > 0) {
      let timer: ReturnType<typeof setTimeout> | undefined
      await Promise.race([Promise.allSettled(waits), new Promise((r) => (timer = setTimeout(r, FLUSH_MS)))])
      clearTimeout(timer)
    }
    try {
      await api.logout()
    } finally {
      set({ ...next, remote: state.remote })
    }
  },
}

// Expired or revoked session: back to «Кто смотрит?» (the Gate redirects).
api.onUnauthorized(() => {
  if (state.status === 'signed-in') set({ status: 'signed-out', user: null, remote: state.remote })
})

export function useSession(): SessionState {
  return useSyncExternalStore(session.subscribe, session.get, session.get)
}
