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
/** i18n/core.ts PROFILE_KEY: the signed-in profile's language as the server last said it. */
const PROFILE_LANGUAGE_KEY = 'vault.profileUiLanguage'

/** The interface language as i18n/core.ts resolves it: the stored pick, else the profile's, else the first ru-… or en-… browser language, else en. */
function bootLanguage(): 'ru' | 'en' {
  try {
    const v = localStorage.getItem(LANGUAGE_KEY)
    if (v === 'ru' || v === 'en') return v
    const p = localStorage.getItem(PROFILE_LANGUAGE_KEY)
    if (p === 'ru' || p === 'en') return p
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

/**
 * Preloads the active language's catalog (a chunk of its own, see
 * i18n/core.ts) so it downloads beside the bundle rather than after it. The
 * build writes the chunks' paths on this script's tag (vite.config.ts).
 */
export function preloadCatalog() {
  if (typeof document === 'undefined') return
  const tag = document.querySelector<HTMLScriptElement>('script[data-i18n-ru]')
  const href = tag?.dataset[bootLanguage() === 'ru' ? 'i18nRu' : 'i18nEn']
  if (!href) return
  const link = document.createElement('link')
  link.rel = 'modulepreload'
  link.crossOrigin = ''
  link.href = href
  document.head.appendChild(link)
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
