// Pure logic of the file browser: sorting, selection, labels and formatting.
// No React here, so it is easy to test and reuse in every view.
import type { FileEntry } from '../../api/types'
import type { FileSort, SortOrder } from '../../api/files'
import { locale, t, type Key } from '../../i18n'

const collators = new Map<string, Intl.Collator>()
/** Natural-order collator in the interface language. */
function collator(): Intl.Collator {
  const l = locale()
  let c = collators.get(l)
  if (!c) collators.set(l, (c = new Intl.Collator(l, { numeric: true, sensitivity: 'base' })))
  return c
}
export const compareNames = (a: string, b: string) => collator().compare(a, b) || (a < b ? -1 : a > b ? 1 : 0)

export function extOf(name: string): string {
  const i = name.lastIndexOf('.')
  return i > 0 && i < name.length - 1 ? name.slice(i + 1).toLowerCase() : ''
}

/** Folders first (as Finder groups them), then by key; ties by name. */
export function sortEntries(entries: FileEntry[], key: FileSort, order: SortOrder): FileEntry[] {
  const dir = order === 'desc' ? -1 : 1
  const by = (a: FileEntry, b: FileEntry): number => {
    switch (key) {
      case 'size':
        return a.size - b.size
      case 'mtime':
        return Date.parse(a.mtime) - Date.parse(b.mtime)
      case 'kind':
        return collator().compare(kindLabel(a), kindLabel(b))
      default:
        return 0
    }
  }
  return [...entries].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
    const k = by(a, b)
    if (k !== 0) return k * dir
    return compareNames(a.name, b.name) * (key === 'name' ? dir : 1)
  })
}

// ---------- Selection ----------

export interface Selection {
  selected: ReadonlySet<string>
  /** Where a Shift range starts. */
  anchor: string | null
  /** The item keyboard actions apply to (Space, Enter, arrows). */
  lead: string | null
}

export const emptySelection: Selection = { selected: new Set(), anchor: null, lead: null }

export const single = (key: string): Selection => ({ selected: new Set([key]), anchor: key, lead: key })

function range(order: string[], from: string, to: string): string[] {
  const a = order.indexOf(from)
  const b = order.indexOf(to)
  if (a < 0 || b < 0) return [to]
  return order.slice(Math.min(a, b), Math.max(a, b) + 1)
}

export interface ClickMods {
  /** ⌘ (Ctrl elsewhere): add or remove one item. */
  toggle?: boolean
  /** Shift: select from the anchor to this item. */
  range?: boolean
}

export function clickSelect(sel: Selection, order: string[], key: string, mods: ClickMods): Selection {
  if (mods.range) {
    const anchor = sel.anchor && order.includes(sel.anchor) ? sel.anchor : key
    const span = range(order, anchor, key)
    const selected = mods.toggle ? new Set([...sel.selected, ...span]) : new Set(span)
    return { selected, anchor, lead: key }
  }
  if (mods.toggle) {
    const selected = new Set(sel.selected)
    if (selected.has(key)) selected.delete(key)
    else selected.add(key)
    return { selected, anchor: key, lead: key }
  }
  return single(key)
}

/** Arrow keys: move the lead by delta (clamped); Shift extends from the anchor. */
export function moveLead(sel: Selection, order: string[], delta: number, extend: boolean): Selection {
  if (order.length === 0) return emptySelection
  const i = sel.lead ? order.indexOf(sel.lead) : -1
  const n = i < 0 ? (delta > 0 ? 0 : order.length - 1) : Math.max(0, Math.min(order.length - 1, i + delta))
  const key = order[n]!
  if (!extend) return single(key)
  const anchor = sel.anchor && order.includes(sel.anchor) ? sel.anchor : (sel.lead ?? key)
  return { selected: new Set(range(order, anchor, key)), anchor, lead: key }
}

export function selectAll(order: string[]): Selection {
  return { selected: new Set(order), anchor: order[0] ?? null, lead: order[order.length - 1] ?? null }
}

/** Drops keys that are no longer listed (after a refresh or a rename). */
export function pruneSelection(sel: Selection, order: string[]): Selection {
  const keep = new Set(order)
  if ([...sel.selected].every((k) => keep.has(k)) && (!sel.lead || keep.has(sel.lead))) return sel
  return {
    selected: new Set([...sel.selected].filter((k) => keep.has(k))),
    anchor: sel.anchor && keep.has(sel.anchor) ? sel.anchor : null,
    lead: sel.lead && keep.has(sel.lead) ? sel.lead : null,
  }
}

// ---------- Labels ----------

const EXT_NAMES: Record<string, string> = { jpg: 'JPEG', jpeg: 'JPEG', tif: 'TIFF', m4v: 'MPEG-4', mp4: 'MPEG-4', m4a: 'MPEG-4', m4b: 'MPEG-4' }
const upper = (ext: string) => EXT_NAMES[ext] ?? ext.toUpperCase()

/** Finder's «Тип» column: «Папка», «Видео MKV», «Субтитры SRT» … */
export function kindLabel(e: Pick<FileEntry, 'name' | 'isDir' | 'kind'>): string {
  if (e.isDir) return t('files.kind.folder')
  const ext = extOf(e.name)
  const x = ext ? upper(ext) : ''
  const of = (plain: Key, withExt: Key) => (x ? t(withExt, { ext: x }) : t(plain))
  // a kind of another build's server only
  if (!__LIBRARY_EDITION__ && e.kind === 'torrent') return t('files.kind.torrent')
  switch (e.kind) {
    case 'video':
      return of('files.kind.video', 'files.kind.videoExt')
    case 'audio':
      return of('files.kind.audio', 'files.kind.audioExt')
    case 'image':
      return of('files.kind.image', 'files.kind.imageExt')
    case 'subtitle':
      return of('files.kind.subtitles', 'files.kind.subtitlesExt')
    case 'text':
      if (ext === 'nfo') return t('files.kind.nfo')
      if (ext === 'txt') return t('files.kind.text')
      return of('files.kind.text', 'files.kind.textExt')
    case 'book':
      return ext === 'pdf' ? t('files.kind.pdf') : of('files.kind.book', 'files.kind.bookExt')
    case 'comic':
      return of('files.kind.comic', 'files.kind.comicExt')
    case 'archive':
      return ext === 'iso' ? t('files.kind.iso') : of('files.kind.archive', 'files.kind.archiveExt')
    default:
      return ext ? t('files.kind.fileExt', { ext: upper(ext) }) : t('files.kind.document')
  }
}

/** Badge text on document icons («MKV», «SRT»). */
export const extBadge = (name: string) => {
  const ext = extOf(name)
  return ext.length > 0 && ext.length <= 5 ? ext.toUpperCase() : ''
}

// ---------- Formatting ----------

const nums = new Map<string, Intl.NumberFormat>()
const num = (n: number, digits: number) => {
  const k = locale() + digits
  let f = nums.get(k)
  if (!f) nums.set(k, (f = new Intl.NumberFormat(locale(), { maximumFractionDigits: digits })))
  return f.format(n)
}

/** Decimal units like Finder: «512 байт», «4 КБ», «1,23 ГБ». */
export function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return t('files.size.zero')
  if (bytes < 1000) return t('files.size.bytes', { count: bytes })
  if (bytes < 1e6) return `${Math.round(bytes / 1e3)} ${t('common.unit.KB')}`
  const units: [number, Key][] = [
    [1e12, 'common.unit.TB'],
    [1e9, 'common.unit.GB'],
    [1e6, 'common.unit.MB'],
  ]
  for (const [u, label] of units) {
    if (bytes >= u) {
      const v = Number((bytes / u).toPrecision(3))
      return `${num(v, 2)} ${t(label)}`
    }
  }
  return t('files.size.bytes', { count: bytes })
}

/** «12 объектов», «1 объект», «3 объекта». */
export const objects = (n: number): string => t('files.objects', { count: n })

const sameDay = (a: Date, b: Date) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
const fmts = new Map<string, Intl.DateTimeFormat>()
/** A cached date formatter in the interface language. */
function fmt(o: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  const k = locale() + JSON.stringify(o)
  let f = fmts.get(k)
  if (!f) fmts.set(k, (f = new Intl.DateTimeFormat(locale(), o)))
  return f
}
const timeFmt = () => fmt({ hour: '2-digit', minute: '2-digit' })

/** «Сегодня, 14:05», «Вчера, 09:12», «25 окт. 2026 г., 14:05». */
export function formatDate(iso: string, now = new Date()): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const y = new Date(now)
  y.setDate(now.getDate() - 1)
  if (sameDay(d, now)) return t('files.date.today', { time: timeFmt().format(d) })
  if (sameDay(d, y)) return t('files.date.yesterday', { time: timeFmt().format(d) })
  return `${fmt({ day: 'numeric', month: 'short', year: 'numeric' }).format(d)}, ${timeFmt().format(d)}`
}

/** Short date for phone rows: «Сегодня», «25.10.2026». */
export function formatShortDate(iso: string, now = new Date()): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  if (sameDay(d, now)) return timeFmt().format(d)
  return fmt({ day: '2-digit', month: '2-digit', year: 'numeric' }).format(d)
}

export const formatLongDate = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : fmt({ day: 'numeric', month: 'long', year: 'numeric', hour: '2-digit', minute: '2-digit' }).format(d)
}

/** 2:49:03 / 42:10. */
export function formatDuration(sec: number): string {
  const s = Math.round(sec)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const r = s % 60
  const pad = (n: number) => String(n).padStart(2, '0')
  return h ? `${h}:${pad(m)}:${pad(r)}` : `${m}:${pad(r)}`
}

/**
 * Finder-style middle truncation: «Interstellar.2…-GRP.mkv». `max` is in the
 * units of `measure` (characters by default, pixels with a canvas measure).
 */
export function fitMiddle(name: string, max: number, measure: (s: string) => number = (s) => s.length): string {
  if (measure(name) <= max) return name
  const ext = extOf(name)
  const minTail = Math.min(name.length - 1, ext ? ext.length + 4 : 4)
  // Grow head and tail together (tail ≈ 40%) while the result fits.
  let best = name.slice(0, 1) + '…'
  for (let total = 2; total < name.length; total++) {
    const tail = Math.max(minTail, Math.round(total * 0.4))
    const head = total - tail
    if (head < 1) continue
    const s = name.slice(0, head).trimEnd() + '…' + name.slice(name.length - tail).trimStart()
    if (measure(s) > max) break
    best = s
  }
  return best
}

// ---------- Paths ----------

export const joinPath = (dir: string, name: string) => (dir ? `${dir}/${name}` : name)
export const parentPath = (p: string) => {
  const i = p.lastIndexOf('/')
  return i < 0 ? '' : p.slice(0, i)
}
export const baseName = (p: string) => p.slice(p.lastIndexOf('/') + 1)

/** URL of a folder in the app: /files/movies/Интерстеллар%20(2014). */
export function folderHref(root: string, path: string): string {
  const parts = [root, ...(path ? path.split('/') : [])].map(encodeURIComponent)
  return `/files/${parts.join('/')}`
}

/** Whether a double-click / Enter on this entry opens the Vault item page. */
export const opensItem = (e: FileEntry) => !e.isDir && e.kind === 'video' && Boolean(e.item)
