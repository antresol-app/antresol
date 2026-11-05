// Bookmarks, highlights and notes kept on this device and synced with the
// server, the way reading progress is (store.ts): every change lands in a
// queue first and shows at once, then goes out; what could not be sent goes
// when the server is back (bindOfflineToSession).
//
// Order is the server's, never this device's clock: each accepted write
// gets the server's time and a revision (`rev`), and the row the server
// answers with replaces this device's copy. A change carries the rev of the
// copy it was made on, so the server can tell a change made offline (a
// delete still wins; nothing brings back a deleted one). A change the
// server refuses (4xx) stays here marked failed and is shown as such.
import { ApiError, getAnnotations, putAnnotation } from '../api/client'
import type { Annotation, AnnotationColor, AnnotationKind } from '../api/types'
import { session } from '../state/session'
import { currentOwner } from './store'

const PENDING_KEY = 'vault.pending-annotations.v2' // gitleaks:allow
export const PENDING_MAX = 500
const cacheKey = (userId: number, itemId: number) => `vault.annotations.v2.${userId}.${itemId}`

export const ANNOTATION_COLORS: AnnotationColor[] = ['yellow', 'green', 'blue', 'pink']
export const EXCERPT_MAX = 1000
export const NOTE_MAX = 10_000

interface PendingAnnotation {
  userId: number
  itemId: number
  /** The change; its `rev` is the revision it was made on. */
  a: Annotation
  /** The server refused it (4xx): not sent again, shown as not saved. */
  failed?: boolean
}

function read<T>(key: string): T[] {
  try {
    const v = JSON.parse(localStorage.getItem(key) ?? '[]') as unknown
    return Array.isArray(v) ? (v as T[]) : []
  } catch {
    return []
  }
}

function write(key: string, list: unknown[]) {
  try {
    if (list.length === 0) localStorage.removeItem(key)
    else localStorage.setItem(key, JSON.stringify(list))
  } catch {
    /* not kept: the live write still goes out */
  }
}

/** `b` (a row from the server) replaces `a`: a later revision, or the same one deleted. */
export function newer(b: Annotation, a: Annotation): boolean {
  return b.rev > a.rev || (b.rev === a.rev && b.deleted && !a.deleted) || (b.rev === a.rev && b.deleted === a.deleted)
}

/** Server rows, one per id: the latest revision of each (a later list wins a tie); tombstones kept. */
export function mergeAnnotations(...lists: Annotation[][]): Annotation[] {
  const by = new Map<string, Annotation>()
  for (const list of lists) {
    for (const a of list) {
      const have = by.get(a.id)
      if (!have || newer(a, have)) by.set(a.id, a)
    }
  }
  return [...by.values()]
}

/** This device's unsent change over the server's row, unless the server already deleted it later. */
function overlay(stored: Annotation[], pending: Annotation[]): Annotation[] {
  const by = new Map(stored.map((a) => [a.id, a]))
  for (const p of pending) {
    const s = by.get(p.id)
    if (s && s.deleted && !p.deleted && s.rev > p.rev) continue
    by.set(p.id, p)
  }
  return [...by.values()]
}

/** What the reader shows: not deleted, in reading order. */
export function liveAnnotations(list: Annotation[]): Annotation[] {
  return list.filter((a) => !a.deleted).sort((a, b) => a.fraction - b.fraction || a.createdAt.localeCompare(b.createdAt) || a.id.localeCompare(b.id))
}

/** A new id: random hex (crypto.randomUUID needs a secure context; a home server is often plain http). */
export function annotationId(): string {
  const b = new Uint8Array(16)
  crypto.getRandomValues(b)
  return Array.from(b, (x) => x.toString(16).padStart(2, '0')).join('')
}

export interface NewAnnotation {
  kind: AnnotationKind
  locator: string
  fraction: number
  label?: string
  excerpt?: string
  note?: string
  color?: AnnotationColor
}

const cutNote = (s: string) => Array.from(s).slice(0, NOTE_MAX).join('')

/** A new annotation made now (rev 0: not on the server yet). */
export function makeAnnotation(n: NewAnnotation, now = new Date()): Annotation {
  const at = now.toISOString()
  return {
    id: annotationId(),
    kind: n.kind,
    locator: n.locator,
    fraction: Math.min(1, Math.max(0, n.fraction || 0)),
    label: n.label ?? '',
    excerpt: Array.from(n.excerpt ?? '').slice(0, EXCERPT_MAX).join(''),
    note: cutNote(n.note ?? ''),
    color: n.kind === 'bookmark' ? '' : (n.color ?? 'yellow'),
    createdAt: at,
    updatedAt: at,
    rev: 0,
    deleted: false,
  }
}

const listeners = new Set<() => void>()
/** Called when any item's annotations change here (the reader re-reads its copy). */
export function subscribeAnnotations(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}
const emit = () => listeners.forEach((fn) => fn())

/** The profile this device's reading belongs to (offline too: the saved items' owner). */
const owner = (): number | null => currentOwner()

const pendingOf = (userId: number, itemId: number) => read<PendingAnnotation>(PENDING_KEY).filter((p) => p.userId === userId && p.itemId === itemId)

/** This device's copy of the item's annotations (tombstones included), with the unsent changes over it. */
export function cachedAnnotations(itemId: number, userId = owner()): Annotation[] {
  if (userId === null) return []
  return overlay(
    read<Annotation>(cacheKey(userId, itemId)),
    pendingOf(userId, itemId).map((p) => p.a),
  )
}

/** The ids of the item's changes the server refused (shown as not saved). */
export function failedAnnotations(itemId: number, userId = owner()): Set<string> {
  if (userId === null) return new Set()
  return new Set(pendingOf(userId, itemId).filter((p) => p.failed).map((p) => p.a.id))
}

function keep(userId: number, itemId: number, list: Annotation[]) {
  write(cacheKey(userId, itemId), mergeAnnotations(read<Annotation>(cacheKey(userId, itemId)), list))
}

/** Changes lost to the queue's cap since the page loaded (a warning, never a delete). */
let lost = 0
export const lostAnnotationChanges = () => lost

/**
 * Queues a change: one per annotation (its latest), so the queue grows only
 * with annotations, not with edits. Over the cap the oldest edits go, with a
 * warning; deletes are never dropped.
 */
function queue(p: PendingAnnotation) {
  let list = [...read<PendingAnnotation>(PENDING_KEY).filter((x) => !(x.userId === p.userId && x.itemId === p.itemId && x.a.id === p.a.id)), p]
  while (list.length > PENDING_MAX) {
    const i = list.findIndex((x) => !x.a.deleted && x !== p)
    if (i < 0) {
      console.warn('vault: the annotation queue is over its cap with deletes only; keeping them all')
      break
    }
    console.warn('vault: annotation queue full, an unsent change is dropped', list[i]!.a.id)
    lost++
    list = list.filter((_, j) => j !== i)
  }
  write(PENDING_KEY, list)
}

const samePending = (x: PendingAnnotation, p: PendingAnnotation) => x.userId === p.userId && x.itemId === p.itemId && x.a.id === p.a.id && x.a.updatedAt === p.a.updatedAt

/** The change reached the server: not queued any more, unless a newer change came meanwhile. */
function settle(p: PendingAnnotation) {
  const list = read<PendingAnnotation>(PENDING_KEY)
  const next = list.filter((x) => !samePending(x, p))
  if (next.length !== list.length) write(PENDING_KEY, next)
}

/** The server refused the change: kept, marked failed, not sent again. */
function fail(p: PendingAnnotation) {
  write(
    PENDING_KEY,
    read<PendingAnnotation>(PENDING_KEY).map((x) => (samePending(x, p) ? { ...x, failed: true } : x)),
  )
}

/** Still no server (or not signed in there): keep it and stop sending for now. */
function isTransient(e: unknown): boolean {
  if (!(e instanceof ApiError)) return true
  return e.status === 0 || e.status === 401 || e.status === 403 || e.status === 408 || e.status === 429 || e.status >= 500
}

async function send(p: PendingAnnotation): Promise<void> {
  try {
    const stored = await putAnnotation(p.itemId, p.a)
    settle(p)
    keep(p.userId, p.itemId, [stored])
  } catch (e) {
    if (isTransient(e)) throw e
    fail(p)
    console.warn('vault: annotation refused', p.a.id, e)
  } finally {
    emit()
  }
}

/**
 * Keeps a change (a new annotation, an edit, a delete) here and sends it.
 * Resolves when it is sent, queued or refused; rejects only when it was
 * none of these (not signed in).
 */
export function writeAnnotation(itemId: number, a: Annotation): Promise<void> {
  const userId = owner()
  if (userId === null || itemId <= 0) return Promise.reject(new Error('not signed in'))
  const p = { userId, itemId, a: { ...a, note: cutNote(a.note) } }
  queue(p)
  emit()
  return send(p).catch((e: unknown) => console.warn('vault: annotation queued', a.id, e))
}

/** The edited copy of `a` (made on its revision; updatedAt is this device's time, a hint). */
export const edited = (a: Annotation, patch: Partial<Pick<Annotation, 'note' | 'color' | 'kind' | 'label'>>, now = new Date()): Annotation => ({
  ...a,
  ...patch,
  updatedAt: now.toISOString(),
})

/** The tombstone of `a`. */
export const deleted = (a: Annotation, now = new Date()): Annotation => ({ ...a, excerpt: '', note: '', label: '', deleted: true, updatedAt: now.toISOString() })

/** Asks the server for the item's annotations; its rows replace this device's copy (the unsent changes stay over them). */
export async function refreshAnnotations(itemId: number, signal?: AbortSignal): Promise<Annotation[]> {
  const userId = owner()
  if (userId === null) return []
  const remote = await getAnnotations(itemId, signal)
  keep(userId, itemId, remote)
  emit()
  return cachedAnnotations(itemId, userId)
}

let sending: Promise<void> | null = null

/** Sends the signed-in profile's queued changes, oldest first; stops while the server is away. */
export function sendPendingAnnotations(): Promise<void> {
  sending ??= (async () => {
    // only a signed-in session can send (offline the owner is known, the server is not)
    const userId = session.get().user?.id ?? null
    if (userId === null) return
    for (const p of read<PendingAnnotation>(PENDING_KEY).filter((x) => x.userId === userId && !x.failed)) {
      try {
        await send(p)
      } catch (e) {
        if (isTransient(e)) return
      }
    }
  })().finally(() => {
    sending = null
  })
  return sending
}

/** The profile's queued changes not refused yet (tests, the «not sent» note). */
export const pendingAnnotations = (): number => {
  const userId = owner()
  return read<PendingAnnotation>(PENDING_KEY).filter((p) => p.userId === userId && !p.failed).length
}
