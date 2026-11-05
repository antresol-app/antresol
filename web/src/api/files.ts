// File browser API (/api/files/*). Mutations are admin-only on the server;
// they go through the shared JSON client, which the CSRF guard expects.
import { qs, request } from './client'
import type { FileEntry, FileListing, FileRoot, FileStat, FileUsage, TrashItem, TrashResult } from './types'

export type FileSort = 'name' | 'size' | 'mtime' | 'kind'
export type SortOrder = 'asc' | 'desc'

export const getRoots = (signal?: AbortSignal) => request<FileRoot[]>('GET', '/api/files/roots', undefined, signal)

export const listDir = (root: string, path: string, q?: string, signal?: AbortSignal) =>
  request<FileListing>('GET', `/api/files/list${qs({ root, path, q })}`, undefined, signal)

export const statFile = (root: string, path: string, signal?: AbortSignal) =>
  request<FileStat>('GET', `/api/files/stat${qs({ root, path })}`, undefined, signal)

export const getUsage = (signal?: AbortSignal) => request<FileUsage>('GET', '/api/files/usage', undefined, signal)

/** Inline content for Quick Look (video, images, text, PDF). */
export const contentUrl = (root: string, path: string) => `/api/files/content${qs({ root, path })}`
/** The file as an attachment («Скачать на это устройство»). */
export const downloadFileUrl = (root: string, path: string) => `/api/files/content${qs({ root, path, download: 1 })}`
/** Server thumbnail at a given width; `thumb` already carries root, path and version. */
export const thumbUrl = (thumb: string, width: number) => `${thumb}&w=${Math.round(width)}`

export const mkdir = (root: string, path: string, name: string) =>
  request<FileEntry>('POST', '/api/files/mkdir', { root, path, name })
/**
 * Rename and move answer `{needsConfirm, warnings}` instead of the entry when
 * the data is shared by a managed download; repeat with confirm to go ahead.
 */
export type ChangeResult = FileEntry | { needsConfirm: true; warnings: string[] }
export const needsConfirm = (r: ChangeResult): r is { needsConfirm: true; warnings: string[] } =>
  'needsConfirm' in r && r.needsConfirm === true
export const renameFile = (root: string, path: string, newName: string, confirm = false) =>
  request<ChangeResult>('POST', '/api/files/rename', { root, path, newName, confirm })
export const moveFile = (root: string, path: string, toRoot: string, toPath: string, confirm = false) =>
  request<ChangeResult>('POST', '/api/files/move', { root, path, toRoot, toPath, confirm })
/** Roots that never accept moved files (the app manages the stream cache). */
export const NOT_MOVE_TARGETS = new Set(['stream-cache'])
export const trashFiles = (root: string, paths: string[], confirm = false) =>
  request<TrashResult>('POST', '/api/files/trash', { root, paths, confirm })

export const getTrash = (signal?: AbortSignal) => request<TrashItem[]>('GET', '/api/files/trash', undefined, signal)
export const restoreTrash = (id: number) => request<{ ok: boolean }>('POST', '/api/files/trash/restore', { id })
export const deleteTrash = (id: number) => request<void>('DELETE', `/api/files/trash/${id}`)
