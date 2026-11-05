// The tracks chosen in the menu go to the server one after another, in the
// order they were made (the last one wins there too), and the next
// episode's player waits for them before it asks what to play
// (Player.tsx): a choice made just before «Следующая серия» is not lost.
import { rememberTracks } from '../api/client'

type Choice = Parameters<typeof rememberTracks>[1]

let chain: Promise<void> = Promise.resolve()

/** Sends `choice` for the item after the ones before it (a failure is dropped: the next choice still goes). */
export function queueRemember(itemId: number, choice: Choice): void {
  chain = chain.then(() => rememberTracks(itemId, choice).catch(() => {}))
}

/** Resolves when every choice queued so far has been sent (or failed). */
export function rememberSettled(): Promise<void> {
  return chain
}
