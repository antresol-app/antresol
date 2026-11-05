// The last answer of a tab's main request (Главная, Музыка, Книги), so
// going back to the tab draws it at once, as Apple's apps keep a tab's
// view, while the page asks again quietly and swaps in the fresh answer.
// Per profile and interface language (the server's shelf titles follow
// it); forgotten when the signed-in profile changes, so nobody else's
// shelves are ever shown, and when its «hide progress» setting changes.
// Главная keeps no positions at all (Home.tsx, positionFree). An answer
// older than KEEP_MS is not drawn, and a page whose refresh of a kept
// answer fails forgets it and shows its error, as a first visit would.
import { getLanguage } from '../i18n'
import { session } from './session'

/** How long a kept answer may be drawn: past this, a revisit starts from the skeleton. */
export const KEEP_MS = 10 * 60_000

const answers = new Map<string, { data: unknown; at: number }>()
const userOf = () => session.get().user
let owner: number | undefined = userOf()?.id
let hidesProgress = Boolean(userOf()?.hideProgress)
/** Bumped whenever the answers are forgotten: a slot from before writes nothing. */
let generation = 0

session.subscribe(() => {
  const user = userOf()
  if (user?.id !== owner || Boolean(user?.hideProgress) !== hidesProgress) {
    answers.clear()
    generation++
    owner = user?.id
    hidesProgress = Boolean(user?.hideProgress)
  }
})

const keyOf = (key: string) => `${owner ?? '-'} ${getLanguage()} ${key}`

/** The answer kept for `key` (undefined: none yet, or another profile's). */
export function lastAnswer<T>(key: string): T | undefined {
  if (owner === undefined) return undefined
  const kept = answers.get(keyOf(key))
  if (!kept || Date.now() - kept.at > KEEP_MS) return undefined
  return kept.data as T
}

/** Forgets the answer kept for `key` (its refresh failed: it is not shown as current again). */
export function forgetAnswer(key: string): void {
  answers.delete(keyOf(key))
}

/** Where a request's answer will be kept: the profile and language of the moment it is asked. */
export interface AnswerSlot {
  readonly key: string
  readonly generation: number
  readonly owner: number | undefined
}

/** Takes the slot when the request starts (see rememberAnswer). */
export function answerSlot(key: string): AnswerSlot {
  return { key: keyOf(key), generation, owner }
}

/**
 * Keeps `data` in its slot. An answer that lands after the profile changed
 * (or its answers were forgotten) is dropped; one that lands after a
 * language switch stays under the language it was asked in.
 */
export function rememberAnswer<T>(slot: AnswerSlot, data: T): void {
  if (slot.owner === undefined || slot.generation !== generation || slot.owner !== owner) return
  answers.set(slot.key, { data, at: Date.now() })
}
