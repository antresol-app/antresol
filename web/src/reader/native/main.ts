// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2026 Antresol Contributors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// The native app's reader: the web reader's foliate path (bookFile → makeBook
// → guardBook → view.open → view.init, styled by readerStyle) inside the
// app's WKWebView. Swift drives it through `window.vault` and hears back
// through the messages in protocol.ts. The book comes from the app's
// vault-reader:// scheme handler, never from the network.
import { makeBook, type RelocateDetail, type View } from '../../vendor/foliate/view.js'
import { bookFile } from '../bookFile'
import { HighlightLayer, readSelection } from '../highlights'
import { DEFAULT_PREFS, normalizePrefs, THEMES, type ReaderPrefs } from '../prefs'
import { applyReaderStyle } from '../readerStyle'
import { guardBook } from '../sanitize'
import { ScreenPageCounter, sectionSizes } from './screenPages'
import {
  marksOnPage,
  pageTurn,
  parseBookmarks,
  parseHighlights,
  parseOpen,
  post,
  relocateMessage,
  roundRect,
  searchMessage,
  tapZone,
  tocMessage,
  type SearchResultMessage,
} from './protocol'

const MAX_HITS = 300

let view: View | null = null
let prefs: ReaderPrefs = { ...DEFAULT_PREFS }
let opening = 0
let searchRun = 0
/** The book's bookmarks (CFI points): a relocate names the ones on the page. */
let bookmarks: string[] = []
/** The book's highlights and notes, drawn in the open view. */
let highlights: ReturnType<typeof parseHighlights> = []
let layer: HighlightLayer | null = null
/** The document with the selection the app was told about. */
let selectedIn: Document | null = null
/** The book's screen pages (re-learned for every layout). */
const screenPages = new ScreenPageCounter()

/** The page on screen in screen pages; nil in scrolled mode or before the section has laid out. */
function screenPage(v: View, d: RelocateDetail) {
  const r = v.renderer
  const index = d.section?.current
  if (!r || r.scrolled || typeof index !== 'number') return null
  screenPages.layout(`${window.innerWidth}x${window.innerHeight}|${JSON.stringify(prefs)}`)
  // the paginator's first and last pages are blank
  return screenPages.count({ index, page: r.page, pages: r.pages - 2 }, sectionSizes(v.book.sections))
}

const compact = () => window.matchMedia('(max-width: 699px)').matches
const reduceMotion = () => window.matchMedia('(prefers-reduced-motion: reduce)').matches

function applyPrefs() {
  // the page around the book (the safe-area margins) is the theme's too
  document.documentElement.style.background = THEMES[prefs.theme].bg
  document.body.style.background = THEMES[prefs.theme].bg
  if (view?.renderer) applyReaderStyle(view.renderer, prefs, { compact: compact(), reduceMotion: reduceMotion() })
}

/** A tap at window x (in the book or on the margins around it). */
function tap(x: number) {
  const zone = tapZone(x, window.innerWidth)
  if (zone === 'left') void view?.goLeft()
  else if (zone === 'right') void view?.goRight()
  post({ type: 'tap', zone })
}

/**
 * ←/→, PageUp/PageDown and Space while the web view has the keyboard (after
 * a click in the book): the page turns here and the app hears a tap, so it
 * counts as a move. Without the focus the app's own key commands turn it.
 */
function onKey(e: KeyboardEvent) {
  if (e.defaultPrevented) return
  const turn = pageTurn(e)
  if (!turn || !view) return
  e.preventDefault()
  if (turn === 'left') void view.goLeft()
  else if (turn === 'right') void view.goRight()
  else if (turn === 'next') void view.next()
  else void view.prev()
  post({ type: 'tap', zone: turn === 'left' || turn === 'prev' ? 'left' : 'right' })
}

function closeView() {
  searchRun++
  layer?.destroy()
  layer = null
  selectedIn = null
  try {
    view?.close()
  } catch {
    /* never opened */
  }
  view?.remove()
  view = null
}

async function open(raw: unknown) {
  const run = ++opening
  closeView()
  try {
    const req = parseOpen(raw)
    prefs = req.prefs
    applyPrefs()
    const res = await fetch(req.url)
    if (!res.ok) throw new Error(`book: HTTP ${res.status}`)
    const file = await bookFile(req.format, await res.blob())
    if (run !== opening) return
    const v = document.createElement('foliate-view') as View
    v.addEventListener('relocate', (e) => {
      const detail = (e as CustomEvent<RelocateDetail>).detail
      post(relocateMessage(detail, bookmarks, screenPage(v, detail)))
    })
    v.addEventListener('load', (e) => {
      const doc = (e as CustomEvent<{ doc: Document }>).detail.doc
      doc.addEventListener('keydown', onKey)
      doc.addEventListener('click', (ev) => {
        if (ev.defaultPrevented || ev.button !== 0) return // links are foliate's
        if (doc.getSelection()?.toString()) return
        const frame = doc.defaultView?.frameElement
        const x = (frame ? frame.getBoundingClientRect().left : 0) + ev.clientX
        // the highlight's own click runs after this one: a tap on one opens its menu instead
        setTimeout(() => {
          if (!layer?.justTapped()) tap(x)
        }, 0)
      })
      // the selection (a long press and its handles): the app shows «Выделить» / «Заметка» once it settles
      const index = (e as CustomEvent<{ index: number }>).detail.index
      let settle: ReturnType<typeof setTimeout> | undefined
      doc.addEventListener('selectionchange', () => {
        clearTimeout(settle)
        settle = setTimeout(() => {
          const sel = readSelection(v, doc, index)
          if (sel) {
            selectedIn = doc
            post({ type: 'selection', cfi: sel.cfi, text: sel.text.slice(0, 1000), rect: roundRect(sel.rect) })
          } else if (selectedIn === doc) {
            selectedIn = null
            post({ type: 'selection', cfi: '', text: '', rect: { x: 0, y: 0, width: 0, height: 0 } })
          }
        }, 350)
      })
    })
    layer = new HighlightLayer(v, (id, locator, rect) => post({ type: 'highlightTap', id, locator, rect: roundRect(rect ?? { x: window.innerWidth / 2, y: window.innerHeight / 2, width: 0, height: 0 }) }))
    document.body.append(v)
    view = v
    // book content is untrusted: no scripts (the page's CSP blocks them too)
    const book = await makeBook(file)
    guardBook(book)
    if (run !== opening) return
    await v.open(book)
    if (run !== opening) return
    applyPrefs()
    post(tocMessage(v.book.toc))
    layer?.set(highlights)
    try {
      await v.init({ lastLocation: req.locator })
    } catch (err) {
      // a position from another device this file cannot resolve: open at the start
      console.warn('saved position not found', err)
      await v.init({})
    }
  } catch (err) {
    if (run !== opening) return
    console.error(err)
    post({ type: 'error', message: err instanceof Error ? err.message : String(err) })
  }
}

/** Searches the book; results arrive in batches, the last one with `done`. An empty query clears the highlights. */
async function search(query: unknown) {
  const v = view
  const run = ++searchRun
  const q = typeof query === 'string' ? query.trim() : ''
  if (!v || !q) {
    v?.clearSearch()
    post(searchMessage([], true))
    return
  }
  const hits: SearchResultMessage[] = []
  let sent = 0
  try {
    for await (const r of v.search({ query: q })) {
      if (run !== searchRun) return // a newer search, or the sheet closed
      if (r === 'done') break
      if ('subitems' in r) for (const s of r.subitems) hits.push({ cfi: s.cfi, excerpt: s.excerpt })
      else if ('cfi' in r) hits.push({ cfi: r.cfi, excerpt: r.excerpt })
      if (hits.length - sent >= 20) {
        post(searchMessage(hits.slice(sent), false))
        sent = hits.length
      }
      if (hits.length >= MAX_HITS) break
    }
  } catch (err) {
    console.error(err)
  }
  if (run === searchRun) post(searchMessage(hits.slice(sent), true))
}

const ignore = (p: Promise<unknown> | undefined) => void p?.catch((err: unknown) => console.warn(err))

const api = {
  open: (req: unknown) => void open(req),
  setPrefs: (p: unknown) => {
    prefs = normalizePrefs(p)
    applyPrefs()
  },
  next: () => ignore(view?.next()),
  prev: () => ignore(view?.prev()),
  goTo: (target: unknown) => {
    if (typeof target === 'string' && target) ignore(view?.goTo(target))
  },
  search: (query: unknown) => void search(query),
  /** The scrubber: 0…1 of the book. */
  goToFraction: (f: unknown) => {
    if (typeof f === 'number' && Number.isFinite(f)) ignore(view?.goToFraction(Math.min(1, Math.max(0, f))))
  },
  /** The book's highlights and notes ({locator, color, note}), drawn over the text. */
  setHighlights: (list: unknown) => {
    highlights = parseHighlights(list)
    layer?.set(highlights)
  },
  /** Drops the selection (a highlight was made of it, or its menu was dismissed). */
  clearSelection: () => {
    selectedIn?.getSelection()?.removeAllRanges()
    selectedIn = null
  },
  /** The book's bookmarks; the app hears which are on the page on screen. */
  setBookmarks: (marks: unknown) => {
    bookmarks = parseBookmarks(marks)
    const at = view?.lastLocation
    if (at?.cfi) post({ type: 'marks', marks: marksOnPage(at.cfi, bookmarks) })
  },
}

declare global {
  interface Window {
    vault: typeof api
  }
}

window.vault = api
// the margins around the page (outside the book's iframe)
document.addEventListener('click', (e) => {
  if (e.button === 0) tap(e.clientX)
})
// the page keys while the web view has the keyboard (section frames get theirs on load)
document.addEventListener('keydown', onKey)
window.matchMedia('(max-width: 699px)').addEventListener('change', applyPrefs)
applyPrefs()
post({ type: 'ready' })
