// The server's folders for the wizard and «Настройки сервера»: the view the
// server returns, the folders of each kind as picked (checked on the server
// as soon as they change, nothing saved yet: added from the folder browser,
// taken out, made the kind's first, marked «Только для взрослых» or «Скрыть
// из общих подборок»), «Создать», and saving, which restarts the server when
// other folders are chosen (marks alone apply at once). Taking out a folder
// the library has titles from asks first: its titles leave the library.
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, errorText } from '../../api/client'
import {
  FOLDER_KINDS,
  checkStorage,
  createFolder,
  getStorage,
  saveStorage,
  waitForServer,
  type FolderKind,
  type FolderPick,
  type FolderPicks,
  type FolderRow,
  type StorageView,
} from '../../api/storage'
import { useT } from '../../i18n'

type Picks = Record<FolderKind, FolderPick[]>

const picksOf = (v: StorageView): Picks => {
  const out = {} as Picks
  for (const k of FOLDER_KINDS) {
    out[k] = v.folders.filter((f) => f.kind === k).map((f) => ({ path: f.path, adult: f.adult, unlisted: f.unlisted }))
  }
  return out
}

const same = (a: Picks, b: Picks) =>
  FOLDER_KINDS.every(
    (k) =>
      a[k].length === b[k].length &&
      a[k].every((p, i) => p.path === b[k][i]!.path && Boolean(p.adult) === Boolean(b[k][i]!.adult) && Boolean(p.unlisted) === Boolean(b[k][i]!.unlisted)),
  )

/** A saved folder the picks take out, with what the library has from it. */
export type Removal = Pick<FolderRow, 'kind' | 'path' | 'indexed'>

export type FolderFlag = 'adult' | 'unlisted'

export interface Storage {
  view: StorageView | null
  /** Each kind's folders as picked (the saved ones until something changes), the first is the destination. */
  picks: Picks | null
  /** Something is picked other than what is saved. */
  dirty: boolean
  /** Other folders than the saved ones (not only other marks): saving restarts the server. */
  moves: boolean
  /** A check, a save or «Создать» is running. */
  busy: boolean
  /** The server is restarting to use the saved folders. */
  restarting: boolean
  error: string
  /** The server did not come back after a restart. */
  slow: boolean
  /** The saved folders the picks take out that the library has titles from: a save asks first. */
  removals: Removal[]
  /** The server refused a save that takes titles out of the library: ask, then save again with `confirm`. */
  confirmNeeded: boolean
  add: (kind: FolderKind, path: string) => Promise<void>
  remove: (kind: FolderKind, path: string) => Promise<void>
  /** Puts another folder in place of `path` (the suggestion «Использовать …»). */
  replace: (kind: FolderKind, path: string, next: string) => Promise<void>
  makePrimary: (kind: FolderKind, path: string) => Promise<void>
  setFlag: (kind: FolderKind, path: string, flag: FolderFlag, on: boolean) => Promise<void>
  create: (path: string) => Promise<void>
  /** Runs the checks again (a fresh write test) for the picked folders. */
  recheck: () => Promise<void>
  /** Drops the picks: the saved folders are shown again. */
  discard: () => Promise<void>
  /** Saves the picks (or the environment's folders with `reset`); `confirm`: titles may leave the library. True when it went through. */
  save: (reset?: boolean, confirm?: boolean) => Promise<boolean>
  dismissConfirm: () => void
}

export function useStorage(): Storage {
  const t = useT()
  const [view, setView] = useState<StorageView | null>(null)
  const [saved, setSaved] = useState<StorageView | null>(null)
  const [busy, setBusy] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [slow, setSlow] = useState(false)
  const [error, setError] = useState('')
  const [confirmNeeded, setConfirmNeeded] = useState(false)
  const alive = useRef(true)

  useEffect(() => {
    alive.current = true
    const ac = new AbortController()
    getStorage(ac.signal).then(
      (v) => {
        if (!Array.isArray(v?.folders)) return // not a server that has the checks
        setView(v)
        setSaved(v)
      },
      (e: unknown) => !ac.signal.aborted && setError(errorText(e, t('storage.loadFailed'))),
    )
    return () => {
      alive.current = false
      ac.abort()
    }
  }, [t])

  const picks = view ? picksOf(view) : null
  const savedPicks = saved ? picksOf(saved) : null
  const dirty = Boolean(picks && savedPicks && !same(picks, savedPicks))
  const moves = Boolean(
    picks && savedPicks && FOLDER_KINDS.some((k) => picks[k].length !== savedPicks[k].length || picks[k].some((p, i) => p.path !== savedPicks[k][i]!.path)),
  )
  const removals: Removal[] =
    picks && saved
      ? saved.folders.filter((f) => f.indexed > 0 && !FOLDER_KINDS.some((k) => picks[k].some((p) => p.path === f.path)))
      : []

  // one request at a time: the picks of a quick second change must not race the first check
  const run = useCallback(async <T,>(call: () => Promise<T>, fallback: string): Promise<T | undefined> => {
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
  }, [])

  const check = useCallback(
    async (next: FolderPicks) => {
      const v = await run(() => checkStorage(next), t('storage.loadFailed'))
      if (v && alive.current) setView(v)
    },
    [run, t],
  )

  /** The picks with one kind's folders changed by `edit`. */
  const change = useCallback(
    async (kind: FolderKind, edit: (list: FolderPick[]) => FolderPick[]) => {
      if (!picks) return
      await check({ ...picks, [kind]: edit(picks[kind].map((p) => ({ ...p }))) })
    },
    [check, picks],
  )

  const add = useCallback((kind: FolderKind, path: string) => change(kind, (l) => [...l, { path }]), [change])
  const remove = useCallback((kind: FolderKind, path: string) => change(kind, (l) => l.filter((p) => p.path !== path)), [change])
  const replace = useCallback(
    (kind: FolderKind, path: string, next: string) => change(kind, (l) => l.map((p) => (p.path === path ? { ...p, path: next } : p))),
    [change],
  )
  const makePrimary = useCallback(
    (kind: FolderKind, path: string) => change(kind, (l) => [...l.filter((p) => p.path === path), ...l.filter((p) => p.path !== path)]),
    [change],
  )
  const setFlag = useCallback(
    (kind: FolderKind, path: string, flag: FolderFlag, on: boolean) => change(kind, (l) => l.map((p) => (p.path === path ? { ...p, [flag]: on } : p))),
    [change],
  )

  const recheck = useCallback(async () => {
    if (picks) await check(picks)
  }, [check, picks])

  const discard = useCallback(async () => {
    if (savedPicks) await check(savedPicks)
  }, [check, savedPicks])

  const create = useCallback(
    async (path: string) => {
      const d = await run(() => createFolder(path), t('storage.loadFailed'))
      if (picks) await check(picks)
      // after the check, which clears the last error
      if (d && d.status !== 'ok' && alive.current) setError(t('storage.createFailed', { error: d.error ?? '' }))
    },
    [check, picks, run, t],
  )

  const save = useCallback(
    async (reset = false, confirm = false) => {
      if (!picks) return false
      setConfirmNeeded(false)
      let refused = false
      const out = await run(async () => {
        try {
          return await saveStorage(reset ? { reset: true, confirmRemove: confirm } : { folders: picks, confirmRemove: confirm })
        } catch (e) {
          // titles would leave the library: the page asks, then saves with confirm
          if (e instanceof ApiError && e.status === 409 && !confirm) refused = true
          throw e
        }
      }, t('storage.saveFailed'))
      if (refused && alive.current) {
        setError('')
        setConfirmNeeded(true)
      }
      if (!out) return false
      const { restarting: r, bootId, removed: _removed, ...v } = out
      if (!r) {
        setView(v)
        setSaved(v)
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
          setSaved(fresh)
        }
        setRestarting(false)
      }
      return true
    },
    [picks, run, t],
  )

  const dismissConfirm = useCallback(() => setConfirmNeeded(false), [])

  return {
    view,
    picks,
    dirty,
    moves,
    busy,
    restarting,
    error,
    slow,
    removals,
    confirmNeeded,
    add,
    remove,
    replace,
    makePrimary,
    setFlag,
    create,
    recheck,
    discard,
    save,
    dismissConfirm,
  }
}
