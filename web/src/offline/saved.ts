// What the app and the service worker share about saved files: the cache
// name and how a saved file is found. Kept free of app code so the service
// worker bundle stays small.

/** Everything saved with «Сохранить офлайн»: details JSON, covers, files. */
export const OFFLINE_CACHE = 'vault-offline-v1'

/** Media files are saved under their path: the player's URLs add `accept=…`, `t=…`. */
export const MEDIA_PATH = /^\/api\/(?:tracks\/\d+\/stream|audio\/\d+|stream\/\d+|images\/[^/]+|comics\/\d+\/page\/\d+)$/

/** A saved file for this URL (the query is ignored), or undefined. */
export async function findSaved(req: RequestInfo | URL): Promise<Response | undefined> {
  const cache = await caches.open(OFFLINE_CACHE)
  return (await cache.match(req, { ignoreSearch: true })) ?? undefined
}

/** The service worker re-reads the saved list on this message. */
export const CHANGED_MESSAGE = 'vault:offline-changed'

/** Everything saved was deleted (another profile): the service worker forgets the list at once. */
export const WIPED_MESSAGE = 'vault:offline-wiped'

/**
 * Present in vault-offline-v1 while the saved items are locked (their profile
 * is signed out): the service worker then serves none of them, also not to
 * direct URLs; requests go to the network, where the server checks the session.
 */
export const LOCK_PATH = '/__vault/offline-locked'
