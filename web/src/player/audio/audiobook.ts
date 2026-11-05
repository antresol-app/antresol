// Audiobooks in the audio engine: one queue item per file of the book, the
// position counted from the start of the book across files, chapters (one
// per file, or several inside one M4B) found by that absolute time.
import { audioFileUrl } from '../../api/client'
import type { BookChapter, BookDetail } from '../../api/types'
import { locate, partStarts } from '../timeline'
import { acceptedCodecs } from './caps'
import { audio, type AudioState, type QueueItem } from './engine'

/** The book's files as queue items, streamed by file id. */
export function bookQueue(detail: BookDetail, accept: string[]): QueueItem[] {
  const starts = partStarts(detail.files)
  const author = detail.authors.join(', ')
  return detail.files.map((f, i) => ({
    key: `b${detail.card.id}f${f.id}`,
    fileId: f.id,
    title: detail.card.title,
    artist: author,
    cover: detail.card.poster,
    durationSec: f.durationMs / 1000,
    src: audioFileUrl(f.id, { accept }),
    bookStartMs: Math.round(starts[i]! * 1000),
    chapters: detail.chapters,
  }))
}

/** Milliseconds from the start of the book: the files before the current one plus its position. */
export function absoluteMs(state: Pick<AudioState, 'index' | 'positionSec'>, files: { durationMs?: number }[]): number {
  const starts = partStarts(files)
  const before = state.index >= 0 ? (starts[state.index] ?? 0) : 0
  return Math.round((before + state.positionSec) * 1000)
}

/** The queue's files as durations (what partStarts/locate take). */
const queueFiles = (s: AudioState) => s.queue.map((q) => ({ durationMs: q.durationSec * 1000 }))

/** The engine's position in the current book (ms), or 0 when no book plays. */
export function bookPositionMs(s: AudioState = audio.get()): number {
  if (s.kind !== 'audiobook' || s.index < 0) return 0
  return absoluteMs(s, queueFiles(s))
}

/** Seeks the current book to `ms` from its start: within the file, or into another one. */
export function seekAbsolute(ms: number) {
  const s = audio.get()
  if (s.queue.length === 0) return
  const { part, offset } = locate(queueFiles(s), Math.max(0, ms) / 1000)
  audio.jumpTo(part, offset)
}

/** A book counts as heard from this share of it, or this close to its end (the server's rule). */
const HEARD_SHARE = 0.98
const HEARD_TAIL_MS = 30_000

/**
 * The book was heard to its end: «Начать сначала» instead of «Продолжить».
 * A position marked finished far from the end (the 92% rule of old) is
 * still a place to continue from.
 */
export function bookHeard(detail: BookDetail): boolean {
  const p = detail.progress
  if (!p?.finished) return false
  const total = detail.durationMs || p.durationMs
  if (!(p.positionMs > 0) || !(total > 0)) return true
  return p.positionMs >= total * HEARD_SHARE || total - p.positionMs <= HEARD_TAIL_MS
}

/** Where a book resumes (ms): the saved position, the start once heard to the end. */
export function bookResumeMs(detail: BookDetail): number {
  const p = detail.progress
  if (!p || bookHeard(detail) || !(p.positionMs > 0)) return 0
  return Math.min(p.positionMs, detail.durationMs || p.positionMs)
}

export const RATES = [0.75, 1, 1.25, 1.5, 1.75, 2, 2.25, 2.5, 2.75, 3] as const
const RATE_KEY = 'vault.audiobook.rate'

/** The audiobook speed chosen last (1 when none or unreadable). */
export function loadRate(): number {
  try {
    const v = Number(localStorage.getItem(RATE_KEY))
    return (RATES as readonly number[]).includes(v) ? v : 1
  } catch {
    return 1
  }
}

/** Remembers the speed and applies it when a book plays (music stays at 1×). */
export function setBookRate(r: number) {
  try {
    localStorage.setItem(RATE_KEY, String(r))
  } catch {
    /* private mode: only this session */
  }
  if (audio.get().kind !== 'music') audio.setRate(r)
}

/** Plays a book from `fromMs` (default: where it was left). */
export function playBook(detail: BookDetail, fromMs?: number) {
  const items = bookQueue(detail, acceptedCodecs())
  if (items.length === 0) return
  const ms = fromMs ?? bookResumeMs(detail)
  const { part, offset } = locate(detail.files, ms / 1000)
  audio.play(items, part, {
    kind: 'audiobook',
    bookId: detail.card.id,
    startSec: offset,
    rate: loadRate(),
    shuffle: false,
  })
}

export interface ChapterPosition {
  chapter: BookChapter
  /** Index in the book's chapter list. */
  index: number
  /** Absolute position now (ms). */
  absMs: number
}

/** The chapter of an audiobook item at `positionSec` into it; null for music or a book without chapters. */
export function chapterAt(it: QueueItem | undefined, positionSec: number): ChapterPosition | null {
  if (!it || it.bookStartMs === undefined || !it.chapters?.length) return null
  const absMs = it.bookStartMs + positionSec * 1000
  const list = it.chapters
  let index = 0
  for (let i = 0; i < list.length; i++) if (list[i]!.startMs <= absMs + 1) index = i
  return { chapter: list[index]!, index, absMs }
}

/** The chapter that holds `ms` of the book (0 for a book without chapters). */
export function chapterIndexAt(chapters: BookChapter[], ms: number): number {
  let index = -1
  for (let i = 0; i < chapters.length; i++) if (chapters[i]!.startMs <= ms + 1) index = i
  return Math.max(0, index)
}
