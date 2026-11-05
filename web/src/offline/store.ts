// «Сохранить офлайн»: albums, books, audiobooks and manga kept on this
// device for listening and reading without the server.
//
// Files go to the Cache Storage `vault-offline-v1` (the service worker serves
// them from there); what is saved is indexed in localStorage
// `vault.offline.v1`. Media is saved as what this browser plays (the
// original, or AAC for a codec it lacks; see `playable`) under its path
// without the query, the rest (details JSON, covers, comic pages) under its
// own URL.
//
// Profiles: the saved items belong to the profile that saved them. Being
// signed out locks them: the offline screen is not shown and a marker in the
// cache (LOCK_PATH) makes the service worker serve none of them, not even to
// direct URLs. Another profile signing in on this device deletes them, so
// nothing of one profile reaches another.
//
// Progress made without the server is queued in `vault.pending-progress.v1`
// with the time it was reached and sent when the server is back, with
// `updatedAt` = that time (corrected by the server clock): the server keeps
// whichever is newer, so an old offline position never overwrites progress
// made on another device.
import { useSyncExternalStore } from 'react'
import { ApiError, comicPageUrl, fileUrl, getAlbum, getBook, getComicPages, imageUrl, saveProgress, saveReading } from '../api/client'
import type { AlbumDetail, BookDetail } from '../api/types'
import { acceptedCodecs } from '../player/audio/caps'
import { session } from '../state/session'
import { getLanguage, t as tr } from '../i18n'
import { sendPendingAnnotations } from './annotations'
import { notifySW } from './register'
import { LOCK_PATH, OFFLINE_CACHE } from './saved'

export type OfflineKind = 'album' | 'book' | 'audiobook' | 'comic'
export type OfflineTarget = { kind: OfflineKind; id: number }

export interface OfflineVolume {
  id: number
  title: string
  poster: string
}

export interface OfflineEntry extends OfflineTarget {
  title: string
  subtitle: string
  /** Image reference as the API gives it (for imageUrl). */
  poster: string
  savedAt: string
  /** Bytes of the saved files (as the server sent them). */
  bytes: number
  /** Every cached URL of the item (removal deletes these). */
  urls: string[]
  /** A comic series: its saved volumes. */
  volumes?: OfflineVolume[]
}

interface Index {
  v: 1
  /** The profile the items belong to. */
  owner: number | null
  /** Its owner signed out: hidden until they sign in again. */
  locked: boolean
  entries: OfflineEntry[]
}

const INDEX_KEY = 'vault.offline.v1'
const PENDING_KEY = 'vault.pending-progress.v1'
const PENDING_MAX = 200

/** Error texts in the interface language (read when thrown). */
export const noSpace = () => tr('offline.noSpace')
export const noStorage = () => tr('offline.noStorage')
const saveFailed = () => tr('offline.saveFailed')

const emptyIndex = (): Index => ({ v: 1, owner: null, locked: false, entries: [] })

function readIndex(): Index {
  try {
    const raw = localStorage.getItem(INDEX_KEY)
    if (!raw) return emptyIndex()
    const v = JSON.parse(raw) as Partial<Index>
    if (v?.v !== 1 || !Array.isArray(v.entries)) return emptyIndex()
    return { v: 1, owner: typeof v.owner === 'number' ? v.owner : null, locked: !!v.locked, entries: v.entries }
  } catch {
    return emptyIndex()
  }
}

function writeIndex(ix: Index) {
  try {
    localStorage.setItem(INDEX_KEY, JSON.stringify(ix))
  } catch {
    /* private mode, full storage: the pages work as without offline */
  }
  emit()
}

const keyOf = (t: OfflineTarget) => `${t.kind}:${t.id}`
const same = (a: OfflineTarget) => (b: OfflineTarget) => a.kind === b.kind && a.id === b.id

/** Saved items of this device (empty while their profile is signed out). */
export function listOffline(): OfflineEntry[] {
  const ix = readIndex()
  return ix.locked ? [] : ix.entries
}

export function isOffline(t: OfflineTarget): boolean {
  return listOffline().some(same(t))
}

/** Something is saved and may be shown (the offline screen instead of «Нет связи»). */
export function offlineAvailable(): boolean {
  return listOffline().length > 0
}

/** Whether this browser can save at all (Cache Storage needs HTTPS or localhost). */
export function offlineSupported(): boolean {
  return typeof caches !== 'undefined' && typeof window !== 'undefined' && window.isSecureContext !== false
}

/** Bytes of the saved items (as the server sent them). */
export function savedBytes(): number {
  return listOffline().reduce((n, e) => n + (e.bytes || 0), 0)
}

/** Whether `url` (any query) is a saved file: the engine seeks inside it instead of asking for a transcode at `t`. */
export function isSavedMedia(url: string): boolean {
  let path: string
  try {
    path = new URL(url, 'http://vault.invalid').pathname
  } catch {
    return false
  }
  return listOffline().some((e) => e.urls.includes(path))
}

/** Bytes the browser uses for this site (all saved files and the app). */
export async function offlineBytes(): Promise<number> {
  try {
    const est = await navigator.storage?.estimate?.()
    if (est?.usage !== undefined) return est.usage
  } catch {
    /* fall through */
  }
  return listOffline().reduce((n, e) => n + (e.bytes || 0), 0)
}

// --- saving ---

export class OfflineError extends Error {}

function isQuota(e: unknown): boolean {
  return e instanceof DOMException && (e.name === 'QuotaExceededError' || e.code === 22)
}

interface Plan {
  entry: Omit<OfflineEntry, 'urls' | 'bytes' | 'savedAt'>
  /** URLs cached with cache.add. */
  plain: string[]
  /** Media paths and the URL each is fetched from (`playable()` for tracks and audiobook files, the book file as is). */
  media: { path: string; fetch: string }[]
}

/**
 * What this browser plays, without the «Музыка вне дома» limit: the
 * original when its codec is accepted (FLAC, MP3, AAC…), else AAC 256k (ALAC
 * in Chrome, APE, WMA); a CUE track comes cut. The engine seeks inside the
 * saved copy (isSavedMedia), so a transcode is seekable too.
 */
const playable = () => `?accept=${encodeURIComponent(acceptedCodecs().join(','))}&maxBitRate=0`

/** A same-origin image to keep (posters from other hosts are left alone). */
const cover = (ref: string | null | undefined): string[] => {
  const u = imageUrl(ref)
  return u && u.startsWith('/') ? [u] : []
}

/** Works out what to save; the details JSON (which tells it) is cached on the way and added to `stored`. */
async function planOf(t: OfflineTarget, cache: Cache, stored: string[], signal?: AbortSignal): Promise<Plan> {
  // the page reads the same JSON offline (the service worker answers from the cache)
  const detail = async <T,>(url: string, get: () => Promise<T>): Promise<T> => {
    const v = await get()
    await cache.put(url, new Response(JSON.stringify(v), { headers: { 'Content-Type': 'application/json' } }))
    stored.push(url)
    return v
  }
  if (t.kind === 'album') {
    const url = `/api/music/albums/${t.id}`
    const a = await detail<AlbumDetail>(url, () => getAlbum(t.id, signal))
    return {
      entry: { kind: t.kind, id: t.id, title: a.card.title, subtitle: a.artist.name, poster: a.card.poster ?? '' },
      plain: [url, ...new Set([...cover(a.card.poster), ...a.tracks.flatMap((s) => cover(s.cover))])],
      media: a.tracks.map((s) => ({ path: `/api/tracks/${s.id}/stream`, fetch: `/api/tracks/${s.id}/stream${playable()}` })),
    }
  }
  const url = `/api/books/${t.id}`
  const b = await detail<BookDetail>(url, () => getBook(t.id, signal))
  const entry = { kind: t.kind, id: t.id, title: b.card.title, subtitle: b.authors.join(', '), poster: b.card.poster ?? '' }
  if (t.kind === 'book') {
    if (!b.readFileId) throw new OfflineError(tr('offline.noBookFile'))
    return { entry, plain: [url, ...cover(b.card.poster)], media: [{ path: fileUrl(b.readFileId), fetch: fileUrl(b.readFileId) }] }
  }
  if (t.kind === 'audiobook') {
    return {
      entry,
      plain: [url, ...cover(b.card.poster)],
      media: b.files.map((f) => ({ path: `/api/audio/${f.id}`, fetch: `/api/audio/${f.id}${playable()}` })),
    }
  }
  // a comic series: every volume, its details (the reader opens /read/:id) and pages
  const plain = [url, ...cover(b.card.poster)]
  for (const v of b.volumes) {
    const vurl = `/api/books/${v.id}`
    await detail(vurl, () => getBook(v.id, signal))
    const purl = `/api/comics/${v.fileId}/pages`
    const pages = await detail(purl, () => getComicPages(v.fileId, signal))
    plain.push(vurl, purl, ...cover(v.poster))
    for (let n = 0; n < pages.count; n++) plain.push(comicPageUrl(v.fileId, n))
  }
  return {
    entry: { ...entry, volumes: b.volumes.map((v) => ({ id: v.id, title: v.subtitle || v.title, poster: v.poster ?? '' })) },
    plain: [...new Set(plain)],
    media: [],
  }
}

/**
 * Saves an item on this device. `onProgress` gets the files done of all;
 * `signal` cancels (what was saved is removed). Throws OfflineError with a
 * message in the interface language («На устройстве не хватает места», …).
 */
export async function saveOffline(t: OfflineTarget, onProgress?: (done: number, total: number) => void, signal?: AbortSignal): Promise<void> {
  if (!offlineSupported()) throw new OfflineError(noStorage())
  const user = session.get().user
  const ix = readIndex()
  if (user && ix.owner !== null && ix.owner !== user.id) await wipeOffline(user.id)
  const first = listOffline().length === 0
  const cache = await caches.open(OFFLINE_CACHE)
  const stored: string[] = []
  let bytes = 0
  try {
    const plan = await planOf(t, cache, stored, signal)
    const total = plan.plain.length + plan.media.length
    let done = 0
    const step = () => onProgress?.(++done, total)
    onProgress?.(0, total)
    for (const u of plan.plain) {
      signal?.throwIfAborted()
      if (!stored.includes(u)) {
        const res = await fetch(u, { credentials: 'same-origin', headers: { 'Accept-Language': getLanguage() }, ...(signal ? { signal } : {}) })
        if (!res.ok) throw new OfflineError(saveFailed())
        bytes += await putCounted(cache, u, res)
        stored.push(u)
      }
      step()
    }
    for (const m of plan.media) {
      signal?.throwIfAborted()
      const res = await fetch(m.fetch, { credentials: 'same-origin', ...(signal ? { signal } : {}) })
      if (!res.ok) throw new OfflineError(saveFailed())
      // under the bare path: the player's URLs carry accept=…/t=…
      bytes += await putCounted(cache, m.path, res)
      stored.push(m.path)
      step()
    }
    if (first) void navigator.storage?.persist?.().catch(() => false)
    const now = readIndex()
    const entry: OfflineEntry = { ...plan.entry, urls: stored, bytes, savedAt: new Date().toISOString() }
    writeIndex({
      ...now,
      owner: user?.id ?? now.owner,
      locked: false,
      entries: [...now.entries.filter((e) => !same(t)(e)), entry],
    })
    notifySW()
  } catch (e) {
    // nothing half-saved stays behind (except files other saved items use)
    await deleteUrls(cache, stored)
    if (signal?.aborted || (e instanceof DOMException && e.name === 'AbortError')) throw e
    if (isQuota(e)) throw new OfflineError(noSpace())
    if (e instanceof OfflineError) throw e
    if (e instanceof ApiError) throw new OfflineError(e.message)
    throw new OfflineError(saveFailed())
  }
}

/**
 * Puts a response, counting its bytes as they stream into the cache (a
 * transcode has no Content-Length); returns the count.
 */
async function putCounted(cache: Cache, key: string, res: Response): Promise<number> {
  const declared = Number(res.headers.get('Content-Length')) || 0
  if (!res.body || typeof TransformStream === 'undefined') {
    await cache.put(key, res)
    return declared
  }
  let n = 0
  const counted = res.body.pipeThrough(
    new TransformStream<Uint8Array, Uint8Array>({
      transform(chunk, ctl) {
        n += chunk.byteLength
        ctl.enqueue(chunk)
      },
    }),
  )
  await cache.put(key, new Response(counted, { status: res.status, statusText: res.statusText, headers: res.headers }))
  return n || declared
}

async function deleteUrls(cache: Cache, urls: string[]) {
  const keep = new Set(readIndex().entries.flatMap((e) => e.urls))
  await Promise.all(urls.filter((u) => !keep.has(u)).map((u) => cache.delete(u).catch(() => false)))
}

/** Removes an item from this device. */
export async function removeOffline(t: OfflineTarget): Promise<void> {
  const ix = readIndex()
  const entry = ix.entries.find(same(t))
  if (!entry) return
  writeIndex({ ...ix, entries: ix.entries.filter((e) => !same(t)(e)) })
  if (typeof caches !== 'undefined') await deleteUrls(await caches.open(OFFLINE_CACHE), entry.urls)
  notifySW()
}

/**
 * Deletes everything saved on this device (another profile signed in): the
 * index first (nothing is listed from here on, `owner` takes over), then the
 * service worker forgets the files, then they are deleted.
 */
export async function wipeOffline(owner: number | null = null): Promise<void> {
  writeIndex({ ...emptyIndex(), owner })
  notifySW(true)
  if (typeof caches !== 'undefined') await caches.delete(OFFLINE_CACHE).catch(() => false)
}

// --- the saving state, for the pages (survives navigation) ---

export type SaveStatus =
  | { state: 'idle' }
  | { state: 'saving'; done: number; total: number }
  | { state: 'saved' }
  | { state: 'error'; message: string }

const saving = new Map<string, { done: number; total: number; ac: AbortController }>()
const errors = new Map<string, string>()
const subs = new Set<() => void>()
let version = 0

function emit() {
  version++
  for (const fn of subs) fn()
}

function subscribe(fn: () => void) {
  subs.add(fn)
  return () => {
    subs.delete(fn)
  }
}

function statusOf(t: OfflineTarget): SaveStatus {
  const k = keyOf(t)
  const s = saving.get(k)
  if (s) return { state: 'saving', done: s.done, total: s.total }
  const err = errors.get(k)
  if (err) return { state: 'error', message: err }
  return isOffline(t) ? { state: 'saved' } : { state: 'idle' }
}

const cachedStatus = new Map<string, { v: number; s: SaveStatus }>()

/** The save state of one item (React). */
export function useOfflineStatus(t: OfflineTarget): SaveStatus {
  const k = keyOf(t)
  const get = () => {
    const c = cachedStatus.get(k)
    if (c && c.v === version) return c.s
    const s = statusOf(t)
    cachedStatus.set(k, { v: version, s })
    return s
  }
  return useSyncExternalStore(subscribe, get, get)
}

/** The saved list (React). */
let listCache: { v: number; list: OfflineEntry[] } | null = null
export function useOfflineList(): OfflineEntry[] {
  const get = () => {
    if (listCache?.v !== version) listCache = { v: version, list: listOffline() }
    return listCache.list
  }
  return useSyncExternalStore(subscribe, get, get)
}

/** Starts saving (no-op while it runs); progress and errors go to useOfflineStatus. */
export function startSaving(t: OfflineTarget) {
  const k = keyOf(t)
  if (saving.has(k)) return
  const ac = new AbortController()
  saving.set(k, { done: 0, total: 0, ac })
  errors.delete(k)
  emit()
  saveOffline(
    t,
    (done, total) => {
      saving.set(k, { done, total, ac })
      emit()
    },
    ac.signal,
  )
    .catch((e: unknown) => {
      if (ac.signal.aborted) return
      errors.set(k, e instanceof Error && e.message ? e.message : saveFailed())
    })
    .finally(() => {
      saving.delete(k)
      emit()
    })
}

export function cancelSaving(t: OfflineTarget) {
  saving.get(keyOf(t))?.ac.abort()
}

export function clearSaveError(t: OfflineTarget) {
  if (errors.delete(keyOf(t))) emit()
}

// --- progress made offline ---

export interface PendingProgress {
  itemId: number
  /** Profile the position belongs to. */
  userId: number
  /** When it was reached (ms, this device's clock). */
  at: number
  positionMs?: number
  durationMs?: number
  locator?: string
  fraction?: number
}

function readPending(): PendingProgress[] {
  try {
    const v = JSON.parse(localStorage.getItem(PENDING_KEY) ?? '[]') as unknown
    return Array.isArray(v) ? (v as PendingProgress[]) : []
  } catch {
    return []
  }
}

function writePending(list: PendingProgress[]) {
  try {
    if (list.length === 0) localStorage.removeItem(PENDING_KEY)
    else localStorage.setItem(PENDING_KEY, JSON.stringify(list.slice(-PENDING_MAX)))
  } catch {
    /* not kept: the live save still goes out */
  }
}

/** The profile this device's listening and reading belongs to: the signed-in one, else the saved items' owner. */
export const currentOwner = (): number | null => session.get().user?.id ?? readIndex().owner

/**
 * Notes a position before it is sent; returns its time for settleProgress.
 * Until settled it stays queued and goes out when the server is back.
 */
export function rememberProgress(p: Omit<PendingProgress, 'userId' | 'at'>): number {
  const at = Date.now()
  const userId = currentOwner()
  if (userId === null || p.itemId <= 0) return at
  writePending([...readPending().filter((x) => !(x.itemId === p.itemId && x.userId === userId)), { ...p, userId, at }])
  // one after another: a later position never lands under an earlier one
  patching = patching.then(() => patchSaved(p, at))
  return at
}

let patching: Promise<void> = Promise.resolve()

/** Positions queued this long ago are dropped (the server will not take them anyway). */
const PENDING_MAX_AGE_MS = 30 * 24 * 3600 * 1000

/** Only a definite answer settles a queued position: done, or the item is gone. */
export function settlesProgress(e: unknown): boolean {
  return e instanceof ApiError && (e.status === 404 || e.status === 410)
}

/** Still no server (or not signed in there): keep it and stop sending for now. */
function isTransient(e: unknown): boolean {
  if (!(e instanceof ApiError)) return true
  return e.status === 0 || e.status === 401 || e.status === 408 || e.status === 429 || e.status >= 500
}

const readingProgress = (p: Omit<PendingProgress, 'userId' | 'at'>, at: number) => {
  if (p.locator !== undefined) {
    const f = Math.min(1, Math.max(0, p.fraction ?? 0))
    return { fraction: f, locator: p.locator, positionMs: Math.round(f * 1e6), durationMs: 1e6, finished: f >= 0.98, updatedAt: new Date(at).toISOString() }
  }
  const pos = p.positionMs ?? 0
  const dur = p.durationMs ?? 0
  const f = dur > 0 ? Math.min(1, pos / dur) : 0
  return { fraction: f, locator: '', positionMs: pos, durationMs: dur, finished: false, updatedAt: new Date(at).toISOString() }
}

/**
 * Writes a position into the saved copy of the item (and of its manga
 * series), so the book reopened without the server starts where it was
 * left. Online, the service worker replaces the copy with the server's.
 */
async function patchSaved(p: Omit<PendingProgress, 'userId' | 'at'>, at: number) {
  if (typeof caches === 'undefined') return
  const url = `/api/books/${p.itemId}`
  const entries = listOffline()
  if (!entries.some((e) => e.urls.includes(url))) return
  try {
    const cache = await caches.open(OFFLINE_CACHE)
    const put = async (key: string, edit: (v: Record<string, unknown>) => void) => {
      const hit = await cache.match(key)
      if (!hit) return
      const v = (await hit.json()) as Record<string, unknown>
      edit(v)
      await cache.put(key, new Response(JSON.stringify(v), { headers: { 'Content-Type': 'application/json' } }))
    }
    const progress = readingProgress(p, at)
    await put(url, (v) => {
      v.progress = progress
    })
    for (const series of entries.filter((e) => e.kind === 'comic' && e.volumes?.some((x) => x.id === p.itemId))) {
      await put(`/api/books/${series.id}`, (v) => {
        for (const vol of (v.volumes as { id: number; progress: number }[] | undefined) ?? []) if (vol.id === p.itemId) vol.progress = progress.fraction
      })
    }
  } catch (e) {
    console.warn('vault: saved copy of', p.itemId, 'not updated', e)
  }
}

/**
 * «Отметить как просмотренное/прочитанное» or «непросмотренное» reached
 * the server: the signed-in profile's positions of the items queued here by
 * `before` (ms, when the mark was asked for) are dropped. The server
 * ignores them now (an «unwatched» mark is kept there as a tombstone), but
 * until sent they would still decide where the item opens here. Another
 * profile's, and a position reached while the mark was on the wire, stay.
 */
export function discardProgress(itemIds: number[], before: number) {
  const user = session.get().user?.id
  if (user === undefined) return
  const ids = new Set(itemIds)
  const list = readPending()
  const next = list.filter((x) => !(ids.has(x.itemId) && x.userId === user && x.at <= before))
  if (next.length !== list.length) writePending(next)
}

/** The save of `itemId` made at `at` reached the server (or was refused): not queued any more. */
export function settleProgress(itemId: number, at: number) {
  const list = readPending()
  const next = list.filter((x) => !(x.itemId === itemId && x.at === at))
  if (next.length !== list.length) writePending(next)
}

/** Server clock minus this device's (ms), from the Date header; 0 when unknown or close. */
async function serverOffset(): Promise<number> {
  try {
    const t0 = Date.now()
    const res = await fetch('/api/health', { cache: 'no-store', credentials: 'same-origin' })
    const t1 = Date.now()
    const d = Date.parse(res.headers.get('Date') ?? '')
    if (!Number.isFinite(d)) return 0
    const off = d + 500 - (t0 + t1) / 2 // Date has whole seconds
    return Math.abs(off) > 5000 ? off : 0
  } catch {
    return 0
  }
}

let sending: Promise<void> | null = null

/** Sends the queued positions of the signed-in profile (each with its time). */
export function sendPendingProgress(): Promise<void> {
  sending ??= (async () => {
    const user = session.get().user
    if (!user) return
    const old = Date.now() - PENDING_MAX_AGE_MS
    for (const p of readPending()) if (p.at < old) settleProgress(p.itemId, p.at)
    const mine = readPending().filter((p) => p.userId === user.id)
    if (mine.length === 0) return
    const offset = await serverOffset()
    for (const p of mine) {
      const updatedAt = new Date(p.at + offset).toISOString()
      try {
        if (p.locator !== undefined) await saveReading(p.itemId, p.locator, p.fraction ?? 0, false, updatedAt)
        else await saveProgress(p.itemId, p.positionMs ?? 0, p.durationMs ?? 0, false, updatedAt)
        settleProgress(p.itemId, p.at)
      } catch (e) {
        if (settlesProgress(e)) {
          settleProgress(p.itemId, p.at) // the item is gone: drop it
          continue
        }
        console.warn('vault: offline position of', p.itemId, 'not sent', e)
        if (isTransient(e)) return // no server (502 from the proxy too): later
      }
    }
  })().finally(() => {
    sending = null
  })
  return sending
}

// --- profiles ---

/** Locks or unlocks the saved items: the index for the app, the marker for the service worker. */
async function setLocked(locked: boolean) {
  const ix = readIndex()
  if (ix.locked !== locked) writeIndex({ ...ix, locked })
  if (typeof caches === 'undefined') return
  try {
    const cache = await caches.open(OFFLINE_CACHE)
    if (locked) await cache.put(LOCK_PATH, new Response('locked', { headers: { 'Content-Type': 'text/plain' } }))
    else await cache.delete(LOCK_PATH)
  } catch (e) {
    console.warn('vault: offline lock marker not written', e)
  }
  notifySW(locked)
}

let bound = false

/**
 * Follows the session: signing in sends queued progress and claims the
 * saved items (another profile's are deleted first); being signed out —
 * also found so at start — locks them.
 */
export function bindOfflineToSession() {
  if (bound) return
  bound = true
  const onChange = () => {
    const { status, user } = session.get()
    if (status === 'signed-in' && user) {
      const ix = readIndex()
      if (ix.owner !== null && ix.owner !== user.id) void wipeOffline(user.id)
      else {
        if (ix.owner !== user.id) writeIndex({ ...ix, owner: user.id })
        // their own profile, checked by the server: unlocked (index and service worker)
        if (ix.entries.length > 0) void setLocked(false)
      }
      void sendPendingProgress()
      void sendPendingAnnotations()
    } else if (status === 'signed-out' || status === 'setup') {
      // signed out, a revoked session, a reset server: hidden until their
      // profile signs in again (offline nobody can prove who holds the device)
      if (readIndex().entries.length > 0) void setLocked(true)
    }
  }
  session.subscribe(onChange)
  onChange()
  window.addEventListener('online', () => {
    if (session.get().status === 'offline') void session.bootstrap()
    else {
      void sendPendingProgress()
      void sendPendingAnnotations()
    }
  })
}
