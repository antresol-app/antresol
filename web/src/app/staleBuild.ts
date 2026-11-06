// A tab opened before a deploy asks for chunks of its own build; once the
// server and the service worker have moved on they can be gone. A route that
// needs one (lazyFrom) reloads the page onto the new build instead of showing
// a failure — at most once a minute, so a chunk that is truly missing ends on
// ChunkBoundary's message, not in a reload loop. Only a caller that needs the
// chunk now decides: there is no global vite:preloadError handler (cancelling
// that event makes the import resolve to undefined, and a background prefetch
// that fails on a blip must not reload the page under what is being typed).
const KEY = 'vault.staleBuildReload'
const WINDOW_MS = 60_000

/** Reloads the page for the new build unless that was just tried; true when it reloads. */
export function reloadForNewBuild(): boolean {
  try {
    const at = Number(sessionStorage.getItem(KEY) ?? 0)
    if (Date.now() - at < WINDOW_MS) return false
    sessionStorage.setItem(KEY, String(Date.now()))
  } catch {
    return false // no storage: no guard, so no reload
  }
  window.location.reload()
  return true
}
