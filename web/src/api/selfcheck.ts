// «Состояние»: the checks only the server can make (/api/admin/selfcheck, admin).
// Each answer is a state and a code with a few figures; the words are the
// page's own (the catalogs), so nothing here is text, and nothing is secret.
import { request } from './client'

/** ok works · warn works, but worse than it could · bad something set up does not work · off not set up (never a fault) · unknown could not be found out. */
export type CheckState = 'ok' | 'warn' | 'bad' | 'off' | 'unknown'

/** The ids the server answers (a newer server may add more: the page ignores what it does not know). */
export type ServerCheckId = 'connection' | 'https' | 'address' | 'data' | 'space' | 'ffmpeg' | 'tmdb' | 'clock' | 'tv' | 'mail'

export interface ServerCheck {
  id: ServerCheckId | (string & {})
  state: CheckState
  /** The situation within the check («proxy», «rejected»). */
  code: string
  /** The figures its sentence uses: strings and numbers. */
  params?: Record<string, string | number>
  /** When an answer asked for on demand (TMDB's key, the mailbox) was got (RFC 3339). */
  at?: string
}

export interface SelfCheckView {
  checkedAt: string
  checks: ServerCheck[]
}

/** What only the browser knows and the checks compare with: its protocol, clock and zone. */
function pageFacts(): string {
  const q = new URLSearchParams({ now: String(Date.now()), tz: String(-new Date().getTimezoneOffset()) })
  if (typeof location !== 'undefined' && location.protocol === 'https:') q.set('https', '1')
  return q.toString()
}

/** The checks; `fresh` asks ffmpeg again instead of the answer kept for ten minutes. */
export const getSelfCheck = (fresh = false, signal?: AbortSignal) =>
  request<SelfCheckView>('GET', `/api/admin/selfcheck?${pageFacts()}${fresh ? '&fresh=1' : ''}`, undefined, signal)

/** The checks that are made when the admin asks: TMDB accepts the key, the mailbox accepts the login. */
export type TestableCheck = 'tmdb' | 'mail'

export const runSelfCheckTest = (id: TestableCheck) => request<ServerCheck>('POST', `/api/admin/selfcheck/${id}`)
