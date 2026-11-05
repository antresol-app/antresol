/// <reference lib="webworker" />
// The service worker (built by vite.sw.config.ts into dist/sw.js; the build
// prepends `self.__VAULT_ASSETS` and `self.__VAULT_BUILD`).
//
// - install: the app shell (every built file and index.html) goes to
//   `vault-shell-<build>`; activate deletes older shells.
// - navigations: the network; index.html from the shell (with the server's
//   headers, CSP included) when it fails, when the proxy answers 502/503/504
//   or, with saved items, after 4 s without an answer.
// - files saved with «Сохранить офлайн» (vault-offline-v1): media and images
//   are served from the cache by path (the query is ignored), `Range` gives
//   206 from the whole file; the API JSON of a saved item goes to the network
//   first and falls back to the cache (also on 502/503/504).
// - nothing else is touched: other origins, non-GET, /api/events (SSE),
//   /rest/ (Subsonic) and /opds (readers) — and API calls of unsaved items.
import { CHANGED_MESSAGE, LOCK_PATH, MEDIA_PATH, OFFLINE_CACHE, WIPED_MESSAGE } from './saved'
import { rangeResponse } from './range'

declare const self: ServiceWorkerGlobalScope & { __VAULT_ASSETS?: string[]; __VAULT_BUILD?: string }

export type Route = 'skip' | 'navigate' | 'shell' | 'media' | 'api'

/** How a request is handled (exported for tests). */
export function routeOf(method: string, url: URL, mode: RequestMode, origin: string): Route {
  if (method !== 'GET' || url.origin !== origin) return 'skip'
  const p = url.pathname
  if (p === '/api/events' || p.startsWith('/rest/') || p === '/rest' || p === '/opds' || p.startsWith('/opds/') || p === '/sw.js') return 'skip'
  if (mode === 'navigate') return 'navigate'
  if (p.startsWith('/api/')) return MEDIA_PATH.test(p) ? 'media' : 'api'
  return 'shell'
}

const SHELL_PREFIX = 'vault-shell-'
const shellName = () => SHELL_PREFIX + (self.__VAULT_BUILD ?? 'dev')
const INDEX = '/index.html'
/** A slow network that is up but delivers nothing: the saved app opens after this long. */
export const NAVIGATE_TIMEOUT_MS = 4000

/** The proxy (NPM) answers these while Vault is stopped or restarting: as good as offline. */
export const isDown = (status: number) => status === 502 || status === 503 || status === 504

/** Paths in vault-offline-v1 (empty while locked); null until read (then every candidate is looked up). */
let saved: Set<string> | null = null
let reading: Promise<Set<string>> | null = null
/** Bumped by every «changed» message: a read that started before it is read again. */
let generation = 0

function readSaved(): Promise<Set<string>> {
  if (reading) return reading
  const gen = generation
  reading = caches
    .open(OFFLINE_CACHE)
    .then((c) => c.keys())
    .then((keys) => {
      const paths = keys.map((r) => new URL(r.url).pathname)
      // locked (their profile is signed out): nothing is served, the server decides
      return new Set(paths.includes(LOCK_PATH) ? [] : paths)
    })
    .then(
      (s) => {
        reading = null
        if (gen !== generation) return readSaved()
        saved = s
        return s
      },
      () => {
        reading = null
        return new Set<string>()
      },
    )
  return reading
}

/** The saved list changed: read it again (also when a read is under way). */
export function savedChanged(wiped = false): Promise<Set<string>> {
  generation++
  if (wiped) saved = new Set()
  return readSaved()
}

/** Tests: forget the saved list. */
export function resetSWForTests() {
  saved = null
  reading = null
  generation = 0
}

/**
 * The whole app shell or nothing: a failed file fails the install, so the
 * old worker and its complete shell stay (the browser tries again later).
 */
export async function install() {
  const cache = await caches.open(shellName())
  const assets = (self.__VAULT_ASSETS ?? []).filter((a) => a !== INDEX)
  await cache.addAll(assets.map((a) => new Request(new URL(a, self.location.origin), { cache: 'reload' })))
  // `/` (not /index.html, which the file server redirects); the server's
  // headers are kept: the app offline runs under the same CSP
  const res = await fetch('/', { cache: 'reload' })
  if (!res.ok) throw new Error(`index.html: ${res.status}`)
  // the body is stored decoded and whole; no cookie is replayed offline
  const headers = new Headers(res.headers)
  for (const h of ['Content-Encoding', 'Content-Length', 'Set-Cookie', 'Transfer-Encoding']) headers.delete(h)
  await cache.put(INDEX, new Response(await res.arrayBuffer(), { status: 200, headers }))
}

async function activate() {
  const names = await caches.keys()
  await Promise.all(names.filter((n) => n.startsWith(SHELL_PREFIX) && n !== shellName()).map((n) => caches.delete(n)))
  await readSaved()
  await self.clients.claim()
}

async function cachedIndex(): Promise<Response | undefined> {
  return (await (await caches.open(shellName())).match(INDEX)) ?? undefined
}

/** The network; the saved app when it fails, the proxy says Vault is down or (with saved items) it hangs. */
export async function navigate(req: Request): Promise<Response> {
  const net = fetch(req)
  net.catch(() => undefined) // a late failure after the timeout is not an error
  let res: Response
  try {
    // a cold start (the launch woke the worker) reads the saved list first
    const list = saved ?? (await readSaved())
    if (list.size > 0) {
      let timer: ReturnType<typeof setTimeout> | undefined
      const slow = new Promise<null>((r) => (timer = setTimeout(() => r(null), NAVIGATE_TIMEOUT_MS)))
      const first = await Promise.race([net, slow])
      clearTimeout(timer)
      if (first === null) return (await cachedIndex()) ?? (await net)
      res = first
    } else res = await net
  } catch (e) {
    const hit = await cachedIndex()
    if (hit) return hit
    throw e
  }
  if (isDown(res.status)) return (await cachedIndex()) ?? res
  return res
}

async function shell(req: Request): Promise<Response> {
  const cache = await caches.open(shellName())
  return (await cache.match(req, { ignoreSearch: true })) ?? fetch(req)
}

/** A saved file by its path (any query); `Range` gets 206 from the whole file. */
export async function media(req: Request): Promise<Response> {
  const cache = await caches.open(OFFLINE_CACHE)
  const hit = await cache.match(req, { ignoreSearch: true })
  if (!hit) return fetch(req)
  const range = req.headers.get('Range')
  if (!range || hit.status !== 200) return hit
  const type = hit.headers.get('Content-Type') ?? 'application/octet-stream'
  const res = rangeResponse(await hit.blob(), range, type)
  const dur = hit.headers.get('X-Content-Duration')
  if (dur) res.headers.set('X-Content-Duration', dur)
  return res
}

/** Saved JSON: the network (refreshing the copy), the copy when Vault cannot be reached. */
export async function api(req: Request): Promise<Response> {
  const cache = await caches.open(OFFLINE_CACHE)
  const key = new URL(req.url).pathname
  let res: Response
  try {
    res = await fetch(req)
  } catch (e) {
    const hit = await cache.match(key)
    if (hit) return hit
    throw e
  }
  if (isDown(res.status)) return (await cache.match(key)) ?? res
  // keep the saved copy fresh (progress, «Нравится»)
  if (res.ok) await cache.put(key, res.clone()).catch(() => undefined)
  return res
}

function onFetch(e: FetchEvent) {
  const req = e.request
  const url = new URL(req.url)
  const route = routeOf(req.method, url, req.mode, self.location.origin)
  switch (route) {
    case 'skip':
      return
    case 'navigate':
      return e.respondWith(navigate(req))
    case 'shell':
      return e.respondWith(shell(req))
    case 'media':
    case 'api': {
      // only saved items; before the list is read, look it up
      if (saved && !saved.has(url.pathname)) return
      const handle = route === 'media' ? media : api
      if (saved) return e.respondWith(handle(req))
      return e.respondWith(readSaved().then((s) => (s.has(url.pathname) ? handle(req) : fetch(req))))
    }
  }
}

// In the app bundle (tests import routeOf) `self` is the window: listen only in the worker.
if (typeof ServiceWorkerGlobalScope !== 'undefined' && self instanceof ServiceWorkerGlobalScope) {
  self.addEventListener('install', (e) => {
    e.waitUntil(install().then(() => self.skipWaiting()))
  })
  self.addEventListener('activate', (e) => e.waitUntil(activate()))
  self.addEventListener('fetch', onFetch)
  self.addEventListener('message', (e) => {
    const type = (e.data as { type?: string } | null)?.type
    if (type === CHANGED_MESSAGE || type === WIPED_MESSAGE) e.waitUntil(savedChanged(type === WIPED_MESSAGE))
  })
}
