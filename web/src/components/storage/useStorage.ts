// The server's folders for the wizard and «Настройки сервера»: the view the
// server returns, the folder picked for each kind (checked on the server as
// soon as it is picked, nothing saved yet), «Создать», and saving, which
// restarts the server when other folders are chosen.
import { useCallback, useEffect, useRef, useState } from 'react'
import { errorText } from '../../api/client'
import {
  FOLDER_KINDS,
  checkStorage,
  createFolder,
  getStorage,
  saveStorage,
  waitForServer,
  type FolderKind,
  type FolderPicks,
  type StorageView,
} from '../../api/storage'
import { useT } from '../../i18n'

const pathsOf = (v: StorageView): Record<FolderKind, string> => {
  const out = {} as Record<FolderKind, string>
  for (const k of FOLDER_KINDS) out[k] = v.folders.find((f) => f.kind === k)?.path ?? ''
  return out
}

export interface Storage {
  view: StorageView | null
  /** The picked folder of each kind (the saved one until another is picked). */
  paths: Record<FolderKind, string> | null
  /** Another folder is picked than the saved one. */
  dirty: boolean
  /** A check, a save or «Создать» is running. */
  busy: boolean
  /** The server is restarting to use the saved folders. */
  restarting: boolean
  error: string
  /** The server did not come back after a restart. */
  slow: boolean
  pick: (kind: FolderKind, path: string) => Promise<void>
  create: (path: string) => Promise<void>
  /** Runs the checks again (a fresh write test) for the picked folders. */
  recheck: () => Promise<void>
  /** Drops the picked folders: the saved ones are shown again. */
  discard: () => Promise<void>
  /** Saves the picked folders (or the environment's with `reset`); true when it went through. */
  save: (reset?: boolean) => Promise<boolean>
}

export function useStorage(): Storage {
  const t = useT()
  const [view, setView] = useState<StorageView | null>(null)
  const [saved, setSaved] = useState<Record<FolderKind, string> | null>(null)
  const [busy, setBusy] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [slow, setSlow] = useState(false)
  const [error, setError] = useState('')
  const alive = useRef(true)

  useEffect(() => {
    alive.current = true
    const ac = new AbortController()
    getStorage(ac.signal).then(
      (v) => {
        if (!Array.isArray(v?.folders)) return // not a server that has the checks
        setView(v)
        setSaved(pathsOf(v))
      },
      (e: unknown) => !ac.signal.aborted && setError(errorText(e, t('storage.loadFailed'))),
    )
    return () => {
      alive.current = false
      ac.abort()
    }
  }, [t])

  const paths = view ? pathsOf(view) : null
  const dirty = Boolean(paths && saved && FOLDER_KINDS.some((k) => paths[k] !== saved[k]))

  // one request at a time: the picks of a quick second change must not race the first check
  const run = useCallback(
    async <T,>(call: () => Promise<T>, fallback: string): Promise<T | undefined> => {
      setBusy(true)
      try {
        const out = await call()
        if (alive.current) setError('')
        return out
      } catch (e) {
        if (alive.current) setError(errorText(e, fallback))
        return undefined
      } finally {
        if (alive.current) setBusy(false)
      }
    },
    [],
  )

  const check = useCallback(
    async (picks: FolderPicks) => {
      const v = await run(() => checkStorage(picks), t('storage.loadFailed'))
      if (v && alive.current) setView(v)
    },
    [run, t],
  )

  const pick = useCallback(
    async (kind: FolderKind, path: string) => {
      if (!paths) return
      await check({ ...paths, [kind]: path })
    },
    [check, paths],
  )

  const recheck = useCallback(async () => {
    if (paths) await check(paths)
  }, [check, paths])

  const discard = useCallback(async () => {
    if (saved) await check(saved)
  }, [check, saved])

  const create = useCallback(
    async (path: string) => {
      const d = await run(() => createFolder(path), t('storage.loadFailed'))
      if (paths) await check(paths)
      // after the check, which clears the last error
      if (d && d.status !== 'ok' && alive.current) setError(t('storage.createFailed', { error: d.error ?? '' }))
    },
    [check, paths, run, t],
  )

  const save = useCallback(
    async (reset = false) => {
      if (!paths) return false
      const out = await run(() => saveStorage(reset ? { reset: true } : { folders: paths }), t('storage.saveFailed'))
      if (!out) return false
      const { restarting: r, bootId, ...v } = out
      if (!r) {
        setView(v)
        setSaved(pathsOf(v))
        return true
      }
      // the server restarts to use the folders: wait for it, then read it again
      setRestarting(true)
      setSlow(false)
      setBusy(true)
      const back = await waitForServer(bootId)
      if (!alive.current) return true
      if (!back) {
        setSlow(true)
        setRestarting(false)
        setBusy(false)
        return false
      }
      const fresh = await run(() => getStorage(), t('storage.loadFailed'))
      if (alive.current) {
        if (fresh) {
          setView(fresh)
          setSaved(pathsOf(fresh))
        }
        setRestarting(false)
      }
      return true
    },
    [paths, run, t],
  )

  return { view, paths, dirty, busy, restarting, error, slow, pick, create, recheck, discard, save }
}
