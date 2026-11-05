// The boot script's one job: start GET /api/me while the app's scripts are
// still on their way. index.html loads it as a separate, tiny async module
// (vite.config.ts, bootScript), so the request goes out as soon as the page
// is parsed instead of after the whole bundle has downloaded and run; the
// session's first getMe() (api/client.ts) takes the answer instead of asking
// again. Over mobile data that takes the bundle's download off the critical
// path of every cold start.
//
// No imports: anything shared with the app would become a chunk of its own
// that the boot script waits for. The request must be the one request()
// sends, so the interface language is worked out here by the same rule as
// i18n/core.ts (a test keeps the two in step).

declare global {
  interface Window {
    /** The boot script's GET /api/me, taken by the first getMe(); null once that has run. */
    __vaultEarlyMe?: Promise<Response> | null
  }
}

/** i18n/core.ts STORAGE_KEY. */
const LANGUAGE_KEY = 'vault.uiLanguage'

/** The interface language as i18n/core.ts resolves it: the stored pick, else the first ru-… or en-… browser language, else en. */
function bootLanguage(): 'ru' | 'en' {
  try {
    const v = localStorage.getItem(LANGUAGE_KEY)
    if (v === 'ru' || v === 'en') return v
  } catch {
    /* storage blocked: follow the system */
  }
  const nav = typeof navigator === 'undefined' ? undefined : navigator
  const preferred = nav?.languages?.length ? nav.languages : nav?.language ? [nav.language] : []
  for (const tag of preferred) {
    const primary = tag.trim().toLowerCase().split(/[-_]/)[0]
    if (primary === 'ru' || primary === 'en') return primary
  }
  return 'en'
}

/** Starts GET /api/me (as request() would send it) and leaves the promise for getMe(). */
export function startEarlyMe() {
  // the app's first getMe ran before this script (a slow boot file): it asked itself
  if (typeof fetch === 'undefined' || '__vaultEarlyMe' in window) return
  const p = fetch('/api/me', { method: 'GET', credentials: 'same-origin', headers: { Accept: 'application/json', 'Accept-Language': bootLanguage() } })
  // a failure is getMe()'s to report (the offline screen); never an unhandled rejection here
  p.catch(() => undefined)
  window.__vaultEarlyMe = p
}
