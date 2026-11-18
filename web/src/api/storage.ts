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

export interface FolderRow extends DirCheck {
  kind: FolderKind
  /** The path inside the container. */
  abs: string
  /** What the environment (VAULT_*_DIR) says. */
  default: string
  /** A folder with files that looks like this kind, offered while this one is missing or empty. */
  suggest?: string
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
}

export type FolderPicks = Partial<Record<FolderKind, string>>

export const getStorage = (signal?: AbortSignal) => request<StorageView>('GET', '/api/settings/storage', undefined, signal)
/** The same view for folders not saved yet (a kind left out keeps its saved one). */
export const checkStorage = (folders?: FolderPicks) => request<StorageView>('POST', '/api/settings/storage/check', { folders: folders ?? {} })
/** Saves the picks; `restarting`: the server is restarting to use them, `bootId`: the run that is about to stop. */
export const saveStorage = (body: { folders: FolderPicks } | { reset: true }) =>
  request<StorageView & { restarting: boolean; bootId?: string }>('PUT', '/api/settings/storage', body)
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
