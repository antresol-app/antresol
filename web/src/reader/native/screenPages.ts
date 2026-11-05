// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2026 Antresol Contributors
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Screen pages for the native reader's «26 из 812» and «Осталось 5 страниц
// в главе». Foliate's `location` is not a page: it counts 1500-byte slices of
// the book's raw (X)HTML, markup included, and Cyrillic text takes two bytes
// a letter, so a novel showed thousands of "pages" that never moved with a
// turn. The paginator knows the real pages of the section on screen only;
// the book's are estimated from the bytes a page holds in the sections laid
// out so far (in this layout), the way a reader app paginates ahead.

/** What the app shows: the page of the book, its pages, and the pages left in the section. */
export interface ScreenPage {
  page: number
  pages: number
  chapterLeft: number
}

/** The paginator's report: the section on screen, its text pages and the page shown (1-based). */
export interface SectionPages {
  index: number
  page: number
  pages: number
}

export class ScreenPageCounter {
  private key = ''
  /** The text pages of each section laid out in this layout (its latest count: fonts and styles can arrive after the first). */
  private learned = new Map<number, { size: number; pages: number }>()
  /** Bytes a page holds; recomputed when the reader enters a section or a section's count changes, so the total stays put while paging. */
  private perPage = 0
  private perPageSection = -1

  /** A layout change (size, prefs) re-paginates: what was learned no longer holds. */
  layout(key: string) {
    if (key === this.key) return
    this.key = key
    this.learned.clear()
    this.perPage = 0
    this.perPageSection = -1
  }

  /**
   * The page on screen. `sizes` are the sections' sizes as foliate weighs
   * them (0 for non-linear ones). Nil when the section has no pages yet
   * (scrolled mode, a section still laying out) or the book has no sizes.
   */
  count(s: SectionPages, sizes: readonly number[]): ScreenPage | null {
    if (!(s.pages >= 1) || !Number.isFinite(s.page)) return null
    const page = Math.min(s.pages, Math.max(1, Math.round(s.page)))
    const chapterLeft = s.pages - page
    const size = sizes[s.index] ?? 0
    // a section of two pages or more is text enough to learn from (a cover or a title page is not)
    if (size > 0 && s.pages >= 2 && this.learned.get(s.index)?.pages !== s.pages) {
      this.learned.set(s.index, { size, pages: s.pages })
      this.perPageSection = -1
    }
    if (this.perPageSection !== s.index || !(this.perPage > 0)) {
      let bytes = 0
      let pages = 0
      for (const l of this.learned.values()) {
        bytes += l.size
        pages += l.pages
      }
      const perPage = pages > 0 ? bytes / pages : size > 0 ? size / s.pages : 0
      if (perPage > 0) {
        this.perPage = perPage
        this.perPageSection = s.index
      }
    }
    const total = sizes.reduce((a, b) => a + b, 0)
    if (!(this.perPage > 0) || !(total > 0)) return null
    const before = sizes.slice(0, s.index).reduce((a, b) => a + b, 0)
    const start = Math.round(before / this.perPage)
    const current = start + page
    const pages = Math.max(Math.round(total / this.perPage), start + s.pages, current)
    return { page: current, pages, chapterLeft }
  }
}

/** The sizes foliate's progress uses: linear sections with a size, 0 for the rest. */
export function sectionSizes(sections: readonly { linear?: string; size?: number }[]): number[] {
  return sections.map((s) => (s.linear !== 'no' && typeof s.size === 'number' && s.size > 0 ? s.size : 0))
}
