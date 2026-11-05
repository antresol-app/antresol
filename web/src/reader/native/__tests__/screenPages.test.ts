import { describe, expect, it } from 'vitest'
import { ScreenPageCounter, sectionSizes } from '../screenPages'

describe('screen pages', () => {
  // a cover (1 page), then three chapters of 3000 bytes a page
  const sizes = [500, 30_000, 60_000, 30_000]

  it('counts the section page by page and estimates the book from it', () => {
    const c = new ScreenPageCounter()
    c.layout('a')
    // chapter 1: 10 pages of 3000 bytes → the book is 120 500 / 3000 ≈ 40 pages
    expect(c.count({ index: 1, page: 1, pages: 10 }, sizes)).toEqual({ page: 1, pages: 40, chapterLeft: 9 })
    expect(c.count({ index: 1, page: 2, pages: 10 }, sizes)).toEqual({ page: 2, pages: 40, chapterLeft: 8 })
    expect(c.count({ index: 1, page: 10, pages: 10 }, sizes)).toEqual({ page: 10, pages: 40, chapterLeft: 0 })
    // chapter 2 starts after it
    expect(c.count({ index: 2, page: 1, pages: 20 }, sizes)).toEqual({ page: 11, pages: 40, chapterLeft: 19 })
  })

  it('does not learn from a cover, and the total stays put while paging in a section', () => {
    const c = new ScreenPageCounter()
    c.layout('a')
    // the cover alone: 500 bytes a page would make 241 pages; it is not learned from
    const cover = c.count({ index: 0, page: 1, pages: 1 }, sizes)
    expect(cover?.page).toBe(1)
    expect(c.count({ index: 1, page: 1, pages: 10 }, sizes)).toEqual({ page: 1, pages: 40, chapterLeft: 9 })
  })

  it('takes a section’s latest count (its styles can lay it out again after the first report)', () => {
    const c = new ScreenPageCounter()
    c.layout('a')
    // the first report before the book's styles: 15 pages; then the real 10
    expect(c.count({ index: 1, page: 1, pages: 15 }, sizes)?.pages).toBe(60)
    expect(c.count({ index: 1, page: 1, pages: 10 }, sizes)).toEqual({ page: 1, pages: 40, chapterLeft: 9 })
  })

  it('starts over when the layout changes', () => {
    const c = new ScreenPageCounter()
    c.layout('small')
    c.count({ index: 1, page: 1, pages: 10 }, sizes)
    c.layout('large')
    // twice the font: 1500 bytes a page
    expect(c.count({ index: 1, page: 3, pages: 20 }, sizes)).toEqual({ page: 3, pages: 80, chapterLeft: 17 })
  })

  it('has nothing to say without pages or sizes', () => {
    const c = new ScreenPageCounter()
    expect(c.count({ index: 0, page: 1, pages: 0 }, sizes)).toBeNull()
    expect(c.count({ index: 0, page: 1, pages: 4 }, [0, 0])).toBeNull()
  })

  it('weighs sections like foliate: non-linear ones count nothing', () => {
    expect(sectionSizes([{ size: 10 }, { linear: 'no', size: 5 }, {}, { size: 0 }])).toEqual([10, 0, 0, 0])
  })
})
