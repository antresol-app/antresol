// The sort and filters of «Управление → Файлы» over every title the server
// lists (GET /api/stats/storage returns them all): pure, for the page and the
// tests. «Watched» is by anyone in the household — what matters for freeing
// space is whether somebody still plays it.
import type { StorageItem } from '../../api/types'
import { MEDIA_SEGMENTS, mediaSegmentOf, type MediaSegment } from '../../components/ViewMenu'

export type FileSegment = MediaSegment
/** Largest first; least recently watched first (never watched before them all); A–Z; newest first. */
export type FileSort = 'size' | 'watched' | 'name' | 'added'
/** idle6 / idle12: nobody has played it for half a year / a year, and it is at least that old. */
export type FileWatch = 'all' | 'watched' | 'never' | 'idle6' | 'idle12'

export const FILE_SEGMENTS = MEDIA_SEGMENTS
export const FILE_SORTS: FileSort[] = ['size', 'watched', 'name', 'added']
export const FILE_WATCHES: FileWatch[] = ['all', 'watched', 'never', 'idle6', 'idle12']

export type FileView = { segment: FileSegment; sort: FileSort; watch: FileWatch }

/** The section of a title (shared with «Загрузки»). */
export const fileSegmentOf = (kind: StorageItem['kind']): FileSegment | null => mediaSegmentOf(kind)

const time = (iso: string | null | undefined) => (iso ? Date.parse(iso) || 0 : 0)

/** Nobody has played it for `months` and it was added before that (so a new title is not «forgotten»). */
export function idleFor(it: StorageItem, months: number, now = Date.now()): boolean {
  const cutoff = new Date(now)
  cutoff.setMonth(cutoff.getMonth() - months)
  const c = cutoff.getTime()
  const added = time(it.addedAt)
  return added > 0 && added < c && (it.lastWatched == null || time(it.lastWatched) < c)
}

export function filterStorage(list: StorageItem[], v: Omit<FileView, 'sort'>, now = Date.now()): StorageItem[] {
  return list.filter((it) => {
    if (v.segment !== 'all' && fileSegmentOf(it.kind) !== v.segment) return false
    switch (v.watch) {
      case 'watched':
        return it.lastWatched != null
      case 'never':
        return it.lastWatched == null
      case 'idle6':
        return idleFor(it, 6, now)
      case 'idle12':
        return idleFor(it, 12, now)
      default:
        return true
    }
  })
}

/** A sorted copy; ties: the largest first, then the id. */
export function sortStorage(list: StorageItem[], sort: FileSort, locale?: string): StorageItem[] {
  const by: Record<FileSort, (a: StorageItem, b: StorageItem) => number> = {
    size: (a, b) => b.size - a.size,
    // never watched (0) first, then the longest ago
    watched: (a, b) => time(a.lastWatched) - time(b.lastWatched),
    name: (a, b) => a.title.localeCompare(b.title, locale, { sensitivity: 'base', numeric: true }),
    added: (a, b) => time(b.addedAt) - time(a.addedAt),
  }
  return [...list].sort((a, b) => by[sort](a, b) || b.size - a.size || a.id - b.id)
}
