// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2026 Antresol Contributors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// The messages between the native app (Swift, VaultReaderWeb) and the reader
// bundle. Swift calls `window.vault.*` (see main.ts); the bundle posts these
// messages to `webkit.messageHandlers.vault`. VaultCore's ReaderMessage
// mirrors the shapes: change both together.
import * as CFI from '../../vendor/foliate/epubcfi.js'
import type { RelocateDetail, SearchExcerpt, TocItem } from '../../vendor/foliate/view.js'
import { normalizePrefs, type ReaderPrefs } from '../prefs'
import type { AnnotationColor, Highlight, ScreenRect } from '../highlights'
import type { ScreenPage } from './screenPages'

// The native reader bundle stays small (no React, no catalogs): the one word
// it shows follows the web view's languages by the shared rule (first ru/en wins, else en).
function untitled(): string {
  const first = (typeof navigator === 'undefined' ? [] : (navigator.languages ?? [navigator.language]))
    .map((l) => (l ?? '').toLowerCase().split(/[-_]/)[0])
    .find((l) => l === 'ru' || l === 'en')
  return first === 'ru' ? 'Без названия' : 'Untitled'
}

export type TapZone = 'left' | 'center' | 'right'

export interface TocEntryMessage {
  label: string
  href: string
  depth: number
}

export interface SearchResultMessage {
  cfi: string
  excerpt: SearchExcerpt
}

export type NativeMessage =
  | { type: 'ready' }
  | { type: 'relocate'; cfi: string; fraction: number; chapter: string; page?: number; pages?: number; chapterLeft?: number; marks?: string[] }
  | { type: 'toc'; items: TocEntryMessage[] }
  | { type: 'search'; results: SearchResultMessage[]; done: boolean }
  | { type: 'tap'; zone: TapZone }
  /** The bookmarks on the page on screen, after `setBookmarks`. */
  | { type: 'marks'; marks: string[] }
  /** Text was selected (a CFI range, its text, where it is in the web view); `cfi: ''` when the selection went away. */
  | { type: 'selection'; cfi: string; text: string; rect: ScreenRect }
  /** A highlight (one of `setHighlights`) was tapped. */
  | { type: 'highlightTap'; id: string; locator: string; rect: ScreenRect }
  | { type: 'error'; message: string }

/** `vault.open(...)`: the book's URL (vault-reader://app/book/<fileId>), its format, the saved position and the prefs. */
export interface OpenRequest {
  url: string
  /** EPUB, FB2, MOBI or AZW3. */
  format: string
  locator: string | null
  prefs: ReaderPrefs
}

/**
 * Where the reader is: the page's range CFI, the fraction, the chapter, the
 * page on screen (screenPages.ts: of the book's estimated `pages`, and the
 * pages left in the section) and the bookmarks on it. Foliate's own
 * `location` is not used: its units are 1500 bytes of markup, not pages.
 */
export function relocateMessage(d: RelocateDetail, bookmarks: readonly string[] = [], screen: ScreenPage | null = null): NativeMessage {
  const msg: NativeMessage = { type: 'relocate', cfi: d.cfi, fraction: Number.isFinite(d.fraction) ? d.fraction : 0, chapter: d.tocItem?.label?.trim() ?? '' }
  if (screen && screen.pages > 0) {
    msg.page = Math.min(screen.pages, Math.max(1, screen.page))
    msg.pages = screen.pages
    msg.chapterLeft = Math.max(0, screen.chapterLeft)
  }
  const marks = marksOnPage(d.cfi, bookmarks)
  if (marks.length) msg.marks = marks
  return msg
}

/**
 * The bookmarks (CFI points) on a page: from its start up to, not including,
 * its end (a mark at a page start can equal the previous page's end after a
 * re-layout; it belongs to the later page). A one-point range holds its point.
 */
export function marksOnPage(range: string, bookmarks: readonly string[]): string[] {
  if (!bookmarks.length || !range) return []
  try {
    const start = CFI.collapse(range)
    const end = CFI.collapse(range, true)
    return bookmarks.filter((m) => {
      try {
        return CFI.compare(m, start) === 0 || (CFI.compare(m, start) > 0 && CFI.compare(m, end) < 0)
      } catch {
        return false
      }
    })
  } catch {
    return []
  }
}

/** `vault.setBookmarks`' argument: the CFI strings, anything else dropped. */
export function parseBookmarks(raw: unknown): string[] {
  return Array.isArray(raw) ? raw.filter((m): m is string => typeof m === 'string' && m.startsWith('epubcfi(')) : []
}

const COLORS: readonly string[] = ['yellow', 'green', 'blue', 'pink']

/** `vault.setHighlights`' argument: {id, locator (a CFI range), color, note}; anything else dropped. */
export function parseHighlights(raw: unknown): Highlight[] {
  if (!Array.isArray(raw)) return []
  const out: Highlight[] = []
  for (const v of raw) {
    if (!v || typeof v !== 'object') continue
    const h = v as Record<string, unknown>
    if (typeof h.locator !== 'string' || !h.locator.startsWith('epubcfi(')) continue
    const color = typeof h.color === 'string' && COLORS.includes(h.color) ? (h.color as AnnotationColor) : 'yellow'
    out.push({ id: typeof h.id === 'string' ? h.id : h.locator, locator: h.locator, color, note: h.note === true })
  }
  return out
}

/** A rectangle with whole pixels (the app lays its menu out by it). */
export const roundRect = (r: ScreenRect): ScreenRect => ({ x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height) })

/** The table of contents, flat with depths (the web panel's rule, EpubReader flattenToc). */
export function tocMessage(toc: TocItem[] | undefined): NativeMessage {
  const items: TocEntryMessage[] = []
  const walk = (list: TocItem[] | undefined, depth: number) => {
    for (const it of list ?? []) {
      items.push({ label: (it.label ?? '').trim() || untitled(), href: it.href, depth })
      if (it.subitems?.length) walk(it.subitems, depth + 1)
    }
  }
  walk(toc, 0)
  return { type: 'toc', items }
}

export function searchMessage(results: SearchResultMessage[], done: boolean): NativeMessage {
  return { type: 'search', results: results.map((r) => ({ cfi: r.cfi, excerpt: { pre: r.excerpt.pre, match: r.excerpt.match, post: r.excerpt.post } })), done }
}

/** Validates `vault.open`'s argument; throws without a url. */
export function parseOpen(raw: unknown): OpenRequest {
  const v = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>
  if (typeof v.url !== 'string' || !v.url) throw new Error('vault.open: missing url')
  return {
    url: v.url,
    format: typeof v.format === 'string' && v.format ? v.format.toUpperCase() : 'EPUB',
    locator: typeof v.locator === 'string' && v.locator ? v.locator : null,
    prefs: normalizePrefs(v.prefs),
  }
}

/**
 * A tap at window x, like Apple Books: only the edges turn the page (a
 * quarter of a phone's width, a fifth of a wider window, so in a two-page
 * spread the outer edges of the spread); everything between, the middle of
 * each column included, shows or hides the panels.
 */
export function tapZone(x: number, width: number): TapZone {
  const edge = Math.max(44, width * (width < 700 ? 0.25 : 0.2))
  if (x < edge) return 'left'
  if (x > width - edge) return 'right'
  return 'center'
}

/** The page turn a key asks for (the web reader's keys, EpubReader pageKey); null for other keys and shortcuts. */
export function pageTurn(e: Pick<KeyboardEvent, 'key' | 'altKey' | 'ctrlKey' | 'metaKey' | 'shiftKey'>): 'left' | 'right' | 'next' | 'prev' | null {
  if (e.altKey || e.ctrlKey || e.metaKey) return null
  switch (e.key) {
    case 'ArrowLeft':
      return 'left'
    case 'ArrowRight':
      return 'right'
    case 'PageUp':
      return 'prev'
    case 'PageDown':
      return 'next'
    case ' ':
      return e.shiftKey ? 'prev' : 'next'
  }
  return null
}

interface MessageHandlers {
  webkit?: { messageHandlers?: { vault?: { postMessage(m: unknown): void } } }
}

/** Sends a message to the app (a no-op outside a WKWebView). */
export function post(msg: NativeMessage) {
  ;(window as unknown as MessageHandlers).webkit?.messageHandlers?.vault?.postMessage(msg)
}
