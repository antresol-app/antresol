// A small shared cache of folder listings. Column view and the list view's
// disclosure triangles show several folders at once; they all read from here,
// and a change (rename, move, trash) marks everything stale so whatever is on
// screen refreshes in place, without flashing empty.
import { useEffect, useState, useSyncExternalStore } from 'react'
import { ApiError } from '../../api/client'
import { listDir, statFile } from '../../api/files'
import type { FileListing, FileStat } from '../../api/types'
import { t } from '../../i18n'

export interface DirState {
  data?: FileListing
  error?: string
  /** HTTP status of the error (404: the folder is gone). */
  status?: number
  loading: boolean
  /** Marked for reload after a change; data is the previous listing. */
  stale?: boolean
}

interface Rec extends DirState {
  stale: boolean
  /** In-flight request; aborted when nobody shows the folder any more. */
  ac?: AbortController
}

/** How many mounted views show each folder (or search). */
const users = new Map<string, number>()

const recs = new Map<string, Rec>()
const listeners = new Set<() => void>()
let version = 0

const keyOf = (root: string, path: string, q = '') => `${root}\u0000${path}\u0000${q}`

function emit() {
  version++
  for (const l of listeners) l()
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => {
    listeners.delete(l)
  }
}

export function peekDir(root: string, path: string, q = ''): DirState | undefined {
  return recs.get(keyOf(root, path, q))
}

/** Loads a listing unless a fresh one is cached or already on its way. */
export function ensureDir(root: string, path: string, q = '') {
  const k = keyOf(root, path, q)
  const r = recs.get(k)
  if (r && (r.loading || !r.stale)) return
  const ac = new AbortController()
  recs.set(k, { ...(r ?? {}), loading: true, stale: false, ac })
  emit()
  listDir(root, path, q || undefined, ac.signal).then(
    (data) => {
      if (recs.get(k)?.ac !== ac) return
      recs.set(k, { data, loading: false, stale: false })
      emit()
    },
    (e: unknown) => {
      if (ac.signal.aborted || recs.get(k)?.ac !== ac) return
      const err = e instanceof ApiError ? e : new ApiError(0, t('files.dir.openFailed'))
      // Keep what was shown before a failed refresh.
      recs.set(k, { data: r?.data, error: err.message, status: err.status, loading: false, stale: false })
      emit()
    },
  )
}

/**
 * A view starts showing a folder; the returned release stops it. When the
 * last view goes (navigation, a new search), a listing still loading is
 * aborted and forgotten, so slow folders and searches do not pile up.
 */
function retain(root: string, path: string, q = '') {
  const k = keyOf(root, path, q)
  users.set(k, (users.get(k) ?? 0) + 1)
  return () => {
    const n = (users.get(k) ?? 1) - 1
    if (n > 0) return void users.set(k, n)
    users.delete(k)
    const r = recs.get(k)
    if (r?.loading) {
      r.ac?.abort()
      if (r.data) recs.set(k, { ...r, loading: false, stale: true, ac: undefined })
      else recs.delete(k)
    }
  }
}

/** Marks cached listings stale (all, or one root); visible folders reload. */
export function invalidateDirs(root?: string) {
  for (const [k, r] of recs) {
    if (!root || k.startsWith(root + '\u0000')) {
      // Search results are cheap to drop; folders stay visible until reloaded.
      if (k.endsWith('\u0000')) recs.set(k, { ...r, stale: true })
      else recs.delete(k)
    }
  }
  emit()
}

/** For tests. */
export function resetDirs() {
  recs.clear()
  users.clear()
  emit()
}

/** Re-renders when any listing changes. */
export const useDirVersion = () => useSyncExternalStore(subscribe, () => version, () => version)

/** A folder's listing (q: search inside it). */
export function useDir(root: string | null, path: string, q = ''): DirState {
  const v = useDirVersion()
  useEffect(() => (root ? retain(root, path, q) : undefined), [root, path, q])
  useEffect(() => {
    if (root) ensureDir(root, path, q)
  }, [root, path, q, v])
  return (root && peekDir(root, path, q)) || { loading: Boolean(root) }
}

/** Several folders at once (expanded rows of the list view). */
export function useDirs(root: string | null, paths: string[]) {
  const v = useDirVersion()
  const key = paths.join('\u0001')
  useEffect(() => {
    if (!root || !key) return
    const releases = key.split('\u0001').map((p) => retain(root, p))
    return () => releases.forEach((r) => r())
  }, [root, key])
  useEffect(() => {
    if (!root) return
    for (const p of key ? key.split('\u0001') : []) ensureDir(root, p)
  }, [root, key, v])
}

/**
 * Stat of one entry for «Свойства». A folder's size is counted on the server
 * in the background (dirSizePending): ask again until it is ready.
 */
export function useStat(
  root: string | null,
  path: string | null,
  refresh = 0,
): { stat?: FileStat; error?: string; /** The folder size never finished: polling stopped. */ gaveUp?: boolean } {
  const [state, setState] = useState<{ key: string; stat?: FileStat; error?: string; gaveUp?: boolean }>({ key: '' })
  const key = root && path !== null ? `${root}\u0000${path}\u0000${refresh}` : ''
  useEffect(() => {
    if (!root || path === null) return
    const ac = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let tries = 0
    const load = () => {
      statFile(root, path, ac.signal).then(
        (stat) => {
          if (!stat.dirSizePending) return setState({ key, stat })
          if (tries++ < 90) {
            setState({ key, stat })
            timer = setTimeout(load, tries < 5 ? 600 : 1500)
          } else setState({ key, stat, gaveUp: true })
        },
        (e: unknown) => {
          if (ac.signal.aborted) return
          setState({ key, error: e instanceof ApiError ? e.message : t('files.info.statFailed') })
        },
      )
    }
    load()
    return () => {
      ac.abort()
      if (timer) clearTimeout(timer)
    }
  }, [root, path, key])
  return state.key === key ? state : {}
}
