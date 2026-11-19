// The server's folders as it sees them, for the first-run wizard and «Настройки
// сервера → Медиатека»: what is under the media folder, whether each library
// folder is there, whether the server can really write to it (the server makes
// and removes a file), and how to fix it when it cannot. Admin only.
import { request } from './client'

export type FolderKind = 'movies' | 'series' | 'music' | 'ebooks' | 'audiobooks' | 'comics'
export const FOLDER_KINDS: FolderKind[] = ['movies', 'series', 'music', 'ebooks', 'audiobooks', 'comics']

export type FolderStatus = 'ok' | 'missing' | 'notdir' | 'unreadable' | 'readonly'
/** Why the server may not write: the owner or mode, a read-only mount, anything else. */
export type FolderWhy = 'denied' | 'readonlyFS' | 'other'

/** What the server found at one path. */
export interface DirCheck {
  path: string
  status: FolderStatus
  /** The visible entries directly in it (up to a limit; `more`: there are more). */
  items: number
  more?: boolean
  /** The operating system's own words for a failure. */
  error?: string
  why?: FolderWhy
}

/** One library folder; a kind has one or more, in order. */
export interface FolderRow extends DirCheck {
  kind: FolderKind
  /** The path inside the container. */
  abs: string
  /** What the environment (VAULT_*_DIR) says for the kind. */
  default: string
  /** The kind's first folder: its downloads and uploads go there. */
  primary: boolean
  /** «Только для взрослых»: what is in it counts as 18+, kid profiles never see it. */
  adult: boolean
  /** «Скрыть из общих подборок»: off the home shelves and the generated channels. */
  unlisted: boolean
  /** How many titles the library has from it (what taking it out removes from the library). */
  indexed: number
  /** A folder with files that looks like this kind, offered while the first one is missing or empty. */
  suggest?: string
}

/** A folder as a save names it: the path as the server keeps it (relative to the media folder, or absolute) and its marks. */
export interface FolderPick {
  path: string
  adult?: boolean
  unlisted?: boolean
}

export interface FolderEntry {
  /** Relative to the media folder (`books/ebooks` when nested). */
  name: string
  items: number
  more?: boolean
}

/** What to tell a person whose folders the server may not write. Commands are in no language. */
export interface StorageFix {
  paths: string[]
  docker: boolean
  /** Who the server runs as. */
  uid: number
  gid: number
  /** The first folder's owner, `uid:gid`. */
  owner?: string
  /** Folders whose place is not there: the mount (the compose volume) is to be fixed, there is no owner to change. */
  mount?: string[]
  /** Gives the folders to the server's user (not for a read-only mount). */
  chown?: string
  /** Runs the server as the folders' owner instead, with the command that hands over its own settings folder. */
  composeUser?: string
  configChown?: string
}

export interface StorageView {
  root: DirCheck
  entries: FolderEntry[]
  folders: FolderRow[]
  transcode: DirCheck
  /** Folders picked here are in use instead of the environment's. */
  custom: boolean
  server: { uid: number; gid: number }
  fix?: StorageFix
  /** The library has files but the media disk looks unmounted: no folder is made while it is. */
  offline: boolean
  /** «Создать» may make a folder (the media folder is there and the disk is not offline). */
  canCreate: boolean
  /** Saving other folders restarts the server by itself. */
  restarts: boolean
  /** Where the folder browser may go: the media folder, then the other folders mounted into the server. */
  places: string[]
}

/** Each kind named gets exactly these folders (a kind left out keeps its own). */
export type FolderPicks = Partial<Record<FolderKind, FolderPick[]>>

/** A folder directly in the one the browser shows. */
export interface BrowseSub {
  name: string
  path: string
  items: number
  more?: boolean
}

/** One folder of the browser: its check (the write test included), what is in it, where else it may go. */
export interface BrowseView {
  places: string[]
  path: string
  /** The folder above, '' at a place. */
  parent: string
  dir: DirCheck
  dirs: BrowseSub[]
  truncated?: boolean
  /** The kind whose folder this already is. */
  library?: FolderKind
}

export const getStorage = (signal?: AbortSignal) => request<StorageView>('GET', '/api/settings/storage', undefined, signal)
/** The same view for folders not saved yet (a kind left out keeps its saved one). */
export const checkStorage = (folders?: FolderPicks) => request<StorageView>('POST', '/api/settings/storage/check', { folders: folders ?? {} })
/** Saves the picks; `restarting`: the server is restarting to use them, `bootId`: the run that is about to stop. */
/** `confirmRemove`: the folders the save takes out may leave the library (their files stay); without it such a save is refused (409). */
export const saveStorage = (body: { folders: FolderPicks; confirmRemove?: boolean } | { reset: true; confirmRemove?: boolean }) =>
  request<StorageView & { restarting: boolean; bootId?: string; removed?: number }>('PUT', '/api/settings/storage', body)
/** The folder browser: `path` absolute, '' for the first place. Only the places and what is in them. */
export const browseFolders = (path: string, signal?: AbortSignal) =>
  request<BrowseView>('GET', '/api/settings/storage/browse' + (path ? '?path=' + encodeURIComponent(path) : ''), undefined, signal)
/** «Создать»: makes the folder under the media folder; says how it went. */
export const createFolder = (path: string) => request<DirCheck>('POST', '/api/settings/storage/create', { path })

/** The first-run wizard is pending from the first administrator until it is finished or skipped. */
export const getSetupWizard = (signal?: AbortSignal) => request<{ pending: boolean }>('GET', '/api/setup/wizard', undefined, signal)
/** Ends the wizard and starts the library scan. */
export const finishSetupWizard = () => request<{ pending: boolean }>('POST', '/api/setup/wizard/finish')

/** How long to wait for a restarting server (ms); tests shorten it. */
export const restartTiming = { first: 1500, every: 1000, max: 60000 }

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms))

/**
 * Waits until a server that was asked to restart answers again: first a
 * pause, then /api/health once a second. `oldBootId` is the run that was
 * asked to stop (the save's answer names it): the old process may go on
 * answering while it shuts down, so only another run counts. Without it
 * (an answer that did not name one) any answer does. False when it does not
 * come back in time.
 */
export async function waitForServer(oldBootId?: string): Promise<boolean> {
  await sleep(restartTiming.first)
  for (let waited = 0; waited <= restartTiming.max; waited += restartTiming.every) {
    try {
      const res = await fetch('/api/health', { credentials: 'same-origin', cache: 'no-store' })
      if (res.ok) {
        if (!oldBootId) return true
        const id = ((await res.json()) as { bootId?: string }).bootId
        if (id && id !== oldBootId) return true
      }
    } catch {
      // not up yet
    }
    await sleep(restartTiming.every)
  }
  return false
}
