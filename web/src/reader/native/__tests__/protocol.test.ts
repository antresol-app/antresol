import { describe, expect, it } from 'vitest'
import { DEFAULT_PREFS } from '../../prefs'
import { marksOnPage, pageTurn, parseBookmarks, parseHighlights, parseOpen, relocateMessage, roundRect, searchMessage, tapZone, tocMessage } from '../protocol'

describe('native reader protocol', () => {
  it('relocate carries the CFI, the fraction and the chapter', () => {
    const msg = relocateMessage({
      cfi: 'epubcfi(/6/4!/4/2/1:0)',
      fraction: 0.25,
      tocItem: { label: '  Глава первая ', href: 'ch1.xhtml' },
      location: { current: 1, next: 2, total: 8 },
    })
    // foliate's location counts 1500-byte slices of markup, not pages: never shown
    expect(msg).toEqual({ type: 'relocate', cfi: 'epubcfi(/6/4!/4/2/1:0)', fraction: 0.25, chapter: 'Глава первая' })
  })

  it('relocate carries the screen page, the pages and the pages left in the chapter', () => {
    const msg = relocateMessage({ cfi: 'epubcfi(/6/4!/4/2/1:0)', fraction: 0.25, tocItem: null }, [], { page: 26, pages: 812, chapterLeft: 5 })
    expect(msg).toEqual({ type: 'relocate', cfi: 'epubcfi(/6/4!/4/2/1:0)', fraction: 0.25, chapter: '', page: 26, pages: 812, chapterLeft: 5 })
  })

  it('relocate names the bookmarks on the page', () => {
    const range = 'epubcfi(/6/4!/4,/2/1:0,/6/1:43)'
    const inside = 'epubcfi(/6/4!/4/4/1:10)'
    const start = 'epubcfi(/6/4!/4/2/1:0)'
    const after = 'epubcfi(/6/4!/4/8/1:0)'
    const otherChapter = 'epubcfi(/6/6!/4/2/1:0)'
    expect(marksOnPage(range, [inside, start, after, otherChapter])).toEqual([inside, start])
    const msg = relocateMessage({ cfi: range, fraction: 0.1, tocItem: null }, [after, inside])
    expect(msg).toEqual({ type: 'relocate', cfi: range, fraction: 0.1, chapter: '', marks: [inside] })
    expect(marksOnPage(range, ['epubcfi(garbage']).length).toBe(0)
    // the page's end belongs to the next page; a one-point range holds its point
    expect(marksOnPage(range, ['epubcfi(/6/4!/4/6/1:43)'])).toEqual([])
    expect(marksOnPage('epubcfi(/6/4!/4,/2/1:0,/2/1:0)', [start])).toEqual([start])
    expect(marksOnPage('', [inside])).toEqual([])
  })

  it('parseBookmarks keeps CFI strings only', () => {
    expect(parseBookmarks(['epubcfi(/6/2)', 'page:3', 7, null])).toEqual(['epubcfi(/6/2)'])
    expect(parseBookmarks('epubcfi(/6/2)')).toEqual([])
  })

  it('relocate without a chapter or a fraction', () => {
    expect(relocateMessage({ cfi: 'epubcfi(/6/2)', fraction: undefined as unknown as number, tocItem: null })).toEqual({
      type: 'relocate',
      cfi: 'epubcfi(/6/2)',
      fraction: 0,
      chapter: '',
    })
  })

  it('toc is flat with depths, like the web panel', () => {
    const msg = tocMessage([
      { label: 'Часть 1', href: 'p1.xhtml', subitems: [{ label: 'Глава 1', href: 'c1.xhtml' }] },
      { label: '  ', href: 'c2.xhtml' },
    ])
    expect(msg).toEqual({
      type: 'toc',
      items: [
        { label: 'Часть 1', href: 'p1.xhtml', depth: 0 },
        { label: 'Глава 1', href: 'c1.xhtml', depth: 1 },
        { label: 'Без названия', href: 'c2.xhtml', depth: 0 },
      ],
    })
    expect(tocMessage(undefined)).toEqual({ type: 'toc', items: [] })
  })

  it('search results carry the CFI and the excerpt', () => {
    const msg = searchMessage([{ cfi: 'epubcfi(/6/4!/4/4,/1:2,/1:6)', excerpt: { pre: 'Это ', match: 'книга', post: ' для' } }], true)
    expect(msg).toEqual({
      type: 'search',
      results: [{ cfi: 'epubcfi(/6/4!/4/4,/1:2,/1:6)', excerpt: { pre: 'Это ', match: 'книга', post: ' для' } }],
      done: true,
    })
    expect(JSON.parse(JSON.stringify(msg))).toEqual(msg)
  })

  it('parseOpen rejects a missing url', () => {
    expect(() => parseOpen({ format: 'EPUB' })).toThrow(/url/)
    expect(() => parseOpen(null)).toThrow(/url/)
    expect(() => parseOpen({ url: '', format: 'EPUB' })).toThrow(/url/)
  })

  it('parseOpen keeps the request and normalizes the prefs', () => {
    expect(parseOpen({ url: 'vault-reader://app/book/7', format: 'fb2', locator: 'epubcfi(/6/2)', prefs: { fontScale: 2.5, theme: 'night' } })).toEqual({
      url: 'vault-reader://app/book/7',
      format: 'FB2',
      locator: 'epubcfi(/6/2)',
      prefs: { ...DEFAULT_PREFS, fontScale: 2, theme: 'night' },
    })
    expect(
      parseOpen({ url: 'u', prefs: { theme: 'quiet', lineSpacing: 'loose', bold: true, justify: false } }).prefs,
    ).toEqual({ ...DEFAULT_PREFS, theme: 'quiet', lineSpacing: 'loose', bold: true, justify: false })
    expect(parseOpen({ url: 'u', prefs: { lineSpacing: 'huge', bold: 'yes', justify: 1 } }).prefs).toEqual(DEFAULT_PREFS)
    expect(parseOpen({ url: 'vault-reader://app/book/7' })).toEqual({
      url: 'vault-reader://app/book/7',
      format: 'EPUB',
      locator: null,
      prefs: DEFAULT_PREFS,
    })
  })

  it('only the edges turn the page; the middle of either column of a spread shows the panels', () => {
    // iPad landscape, a spread: the columns' middles are at about a quarter and three quarters
    expect(tapZone(10, 1194)).toBe('left')
    expect(tapZone(230, 1194)).toBe('left')
    expect(tapZone(300, 1194)).toBe('center')
    expect(tapZone(597, 1194)).toBe('center')
    expect(tapZone(900, 1194)).toBe('center')
    expect(tapZone(970, 1194)).toBe('right')
    // a phone: a quarter at either side
    expect(tapZone(90, 402)).toBe('left')
    expect(tapZone(110, 402)).toBe('center')
    expect(tapZone(290, 402)).toBe('center')
    expect(tapZone(310, 402)).toBe('right')
  })

  it('page keys turn the page, shortcuts do not', () => {
    const key = (k: string, mods: Partial<KeyboardEvent> = {}) => ({ key: k, altKey: false, ctrlKey: false, metaKey: false, shiftKey: false, ...mods })
    expect(pageTurn(key('ArrowLeft'))).toBe('left')
    expect(pageTurn(key('ArrowRight'))).toBe('right')
    expect(pageTurn(key(' '))).toBe('next')
    expect(pageTurn(key(' ', { shiftKey: true }))).toBe('prev')
    expect(pageTurn(key('PageDown'))).toBe('next')
    expect(pageTurn(key('PageUp'))).toBe('prev')
    expect(pageTurn(key('ArrowRight', { metaKey: true }))).toBeNull()
    expect(pageTurn(key('w', { metaKey: true }))).toBeNull()
    expect(pageTurn(key('a'))).toBeNull()
  })

  it('setHighlights takes CFI ranges with a preset colour', () => {
    expect(
      parseHighlights([
        { id: 'h1', locator: 'epubcfi(/6/4!/4,/1:0,/1:5)', color: 'green', note: true },
        { locator: 'epubcfi(/6/8!/4,/1:0,/1:5)', color: 'purple' },
        { locator: 'page:3', color: 'blue' },
        'epubcfi(/6/4)',
        null,
      ]),
    ).toEqual([
      { id: 'h1', locator: 'epubcfi(/6/4!/4,/1:0,/1:5)', color: 'green', note: true },
      { id: 'epubcfi(/6/8!/4,/1:0,/1:5)', locator: 'epubcfi(/6/8!/4,/1:0,/1:5)', color: 'yellow', note: false },
    ])
    expect(parseHighlights('x')).toEqual([])
    expect(roundRect({ x: 1.4, y: 2.6, width: 10.5, height: 3 })).toEqual({ x: 1, y: 3, width: 11, height: 3 })
  })
})
