// Whether a newer Antresol is out, for the administrator's banner
// (components/UpdateBanner): GET /api/updates at most every six hours per
// device (the answer is kept in localStorage), and the release the admin
// hid stays hidden — the next one shows again. Storage may be missing or
// throw (a private window): then it asks on every load and «Скрыть» lasts
// until the page reloads.
import { getCoreUpdate } from '../api/client'
import type { CoreUpdate } from '../api/types'

const CHECK = 'vault.updateCheck'
const HIDDEN = 'vault.updateHidden'
export const CHECK_EVERY_MS = 6 * 60 * 60 * 1000

interface Kept {
  at: number
  latest: string
  newer: boolean
}

function read<T>(key: string): T | null {
  try {
    return JSON.parse(localStorage.getItem(key) ?? 'null') as T | null
  } catch {
    return null
  }
}

function write(key: string, v: unknown) {
  try {
    localStorage.setItem(key, JSON.stringify(v))
  } catch {
    // no storage: asked again next time
  }
}

let hiddenNow = ''

/** The newer release's tag to tell the admin about ('' none, or hidden). */
export async function newerRelease(now = Date.now(), signal?: AbortSignal): Promise<string> {
  let kept = read<Kept>(CHECK)
  if (!kept || typeof kept.at !== 'number' || now - kept.at >= CHECK_EVERY_MS || now < kept.at) {
    const u: CoreUpdate = await getCoreUpdate(signal)
    if (!u.checked) return '' // GitHub did not answer: ask again next load
    kept = { at: now, latest: u.latest, newer: u.newer }
    write(CHECK, kept)
  }
  if (!kept.newer || !kept.latest) return ''
  const hidden = read<string>(HIDDEN) ?? hiddenNow
  return hidden === kept.latest ? '' : kept.latest
}

/** «Скрыть»: this release is not told about again on this device. */
export function hideRelease(tag: string) {
  hiddenNow = tag
  write(HIDDEN, tag)
}
