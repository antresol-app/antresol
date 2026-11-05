// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2026 Antresol Contributors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Highlights in a foliate view, shared by the web reader and the native
// app's reader bundle: draws the highlights and notes (foliate's overlay
// API: addAnnotation → draw-annotation, redrawn for every section that
// loads), reports a tap on one, and reads the reader's text selection as a
// CFI range with its text and where it is on screen.
import type { View } from '../vendor/foliate/view.js'
import { Overlayer } from '../vendor/foliate/overlayer.js'

/** The four highlight colours (a bookmark has none); api/types.ts re-exports it. */
export type AnnotationColor = 'yellow' | 'green' | 'blue' | 'pink'

/** The highlight fill of each preset, drawn at the overlayer's 30 % over every page theme. */
export const HIGHLIGHT_FILL: Record<AnnotationColor, string> = {
  yellow: '#ffd60a',
  green: '#30d158',
  blue: '#40a9ff',
  pink: '#ff6fae',
}

export interface Highlight {
  /** The annotation's id. */
  id: string
  /** A CFI range. */
  locator: string
  color: AnnotationColor | ''
  /** A note: underlined as well. */
  note?: boolean
}

/** A rectangle in the window's coordinates. */
export interface ScreenRect {
  x: number
  y: number
  width: number
  height: number
}

/** A range's box in the window (it lives in a section's iframe). */
export function screenRect(range: Range): ScreenRect {
  const r = range.getBoundingClientRect()
  const frame = range.startContainer.ownerDocument?.defaultView?.frameElement
  const off = frame ? frame.getBoundingClientRect() : { left: 0, top: 0 }
  return { x: r.left + off.left, y: r.top + off.top, width: r.width, height: r.height }
}

export interface TextSelection {
  /** The CFI range of the selected text. */
  cfi: string
  text: string
  rect: ScreenRect
}

/** The selection in a section's document as a CFI range, or null when there is none. */
export function readSelection(view: View, doc: Document, index: number): TextSelection | null {
  const sel = doc.getSelection()
  if (!sel || sel.isCollapsed || sel.rangeCount === 0) return null
  const text = sel.toString().replace(/\s+/g, ' ').trim()
  if (!text) return null
  const range = sel.getRangeAt(0)
  try {
    return { cfi: view.getCFI(index, range), text, rect: screenRect(range) }
  } catch {
    return null
  }
}

/**
 * The highlights drawn in `view`, kept by annotation id. Foliate draws one
 * overlay per CFI range: of several annotations on the same text the last
 * in the list shows. `set` replaces the list (only what changed is
 * redrawn); `onTap` hears a click on one (its id, locator and box).
 */
export class HighlightLayer {
  #view: View
  #drawn = new Map<string, Highlight>()
  /** The highlight each drawn range shows (the last one on it). */
  #shown = new Map<string, Highlight>()
  #off: () => void
  /** When a highlight was last clicked (the reader's own tap handler skips that click). */
  tappedAt = 0

  constructor(view: View, onTap: (id: string, locator: string, rect: ScreenRect | null) => void) {
    this.#view = view
    const draw = (e: Event) => {
      const { draw, annotation } = (e as CustomEvent<{ draw: (f: unknown, o: unknown) => void; annotation: { value: string } }>).detail
      const h = this.#shown.get(annotation.value)
      const color = HIGHLIGHT_FILL[(h?.color || 'yellow') as AnnotationColor]
      draw(h?.note ? noteMark : Overlayer.highlight, { color })
    }
    // a section loaded: its overlay is new and empty
    const created = (e: Event) => {
      const { index } = (e as CustomEvent<{ index: number }>).detail
      for (const h of this.#shown.values()) {
        if (sectionOf(view, h.locator) === index) void this.#add(h)
      }
    }
    const shown = (e: Event) => {
      const { value, range } = (e as CustomEvent<{ value: string; range?: Range }>).detail
      const h = this.#shown.get(value)
      if (!h) return
      this.tappedAt = Date.now()
      onTap(h.id, value, range ? screenRect(range) : null)
    }
    view.addEventListener('draw-annotation', draw)
    view.addEventListener('create-overlay', created)
    view.addEventListener('show-annotation', shown)
    this.#off = () => {
      view.removeEventListener('draw-annotation', draw)
      view.removeEventListener('create-overlay', created)
      view.removeEventListener('show-annotation', shown)
    }
  }

  #add(h: Highlight) {
    return this.#view.addAnnotation({ value: h.locator }).catch((e: unknown) => console.warn('highlight not drawn', h.locator, e))
  }

  set(list: Highlight[]) {
    const ranges = list.filter((h) => h.locator.startsWith('epubcfi('))
    this.#drawn = new Map(ranges.map((h) => [h.id, h]))
    const shown = new Map(ranges.map((h) => [h.locator, h]))
    const before = this.#shown
    this.#shown = shown
    for (const [loc] of before) {
      if (!shown.has(loc)) void this.#view.deleteAnnotation({ value: loc }).catch(() => undefined)
    }
    for (const [loc, h] of shown) {
      const old = before.get(loc)
      if (!old || old.id !== h.id || old.color !== h.color || !!old.note !== !!h.note) void this.#add(h)
    }
  }

  /** The highlight drawn with this id (tests, taps). */
  has(id: string): boolean {
    return this.#drawn.has(id)
  }

  /** The click that just happened landed on a highlight. */
  justTapped(): boolean {
    return Date.now() - this.tappedAt < 300
  }

  destroy() {
    this.#off()
  }
}

/** A note: its highlight with a line under it. */
function noteMark(rects: DOMRectList, options: { color: string }): SVGElement {
  const g = Overlayer.highlight(rects, options)
  const line = Overlayer.underline(rects, { color: options.color, width: 2 })
  line.style.opacity = '1'
  const both = document.createElementNS('http://www.w3.org/2000/svg', 'g')
  both.append(g, line)
  return both
}

/** The section a CFI is in, or -1. */
function sectionOf(view: View, cfi: string): number {
  try {
    return view.resolveNavigation(cfi)?.index ?? -1
  } catch {
    return -1
  }
}
