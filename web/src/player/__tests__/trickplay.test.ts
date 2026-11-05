import { afterEach, describe, expect, it, vi } from 'vitest'
import { preloadSheets, tileAt, tileStyle } from '../trickplay'
import type { TrickplayIndex } from '../../api/types'

const ix: TrickplayIndex = { version: 1, key: 'k', interval: 10, width: 320, height: 180, columns: 10, rows: 10, count: 250, sheets: 3 }

describe('tileAt', () => {
  it('picks the sheet, column and row of a time', () => {
    expect(tileAt(ix, 0)).toEqual({ sheet: 0, col: 0, row: 0 })
    expect(tileAt(ix, 9.9)).toEqual({ sheet: 0, col: 0, row: 0 })
    expect(tileAt(ix, 10)).toEqual({ sheet: 0, col: 1, row: 0 })
    expect(tileAt(ix, 125)).toEqual({ sheet: 0, col: 2, row: 1 })
    expect(tileAt(ix, 1000)).toEqual({ sheet: 1, col: 0, row: 0 })
    expect(tileAt(ix, 2049)).toEqual({ sheet: 2, col: 4, row: 0 })
  })
  it('keeps to the frames there are', () => {
    expect(tileAt(ix, 99999)).toEqual({ sheet: 2, col: 9, row: 4 })
    expect(tileAt(ix, -5)).toEqual({ sheet: 0, col: 0, row: 0 })
    expect(tileAt({ ...ix, count: 0 }, 5)).toBeNull()
  })
})

describe('tileStyle', () => {
  it('crops the tile out of the sheet at the shown size', () => {
    const s = tileStyle(ix, { sheet: 1, col: 3, row: 2 }, '/s/1.jpg', 160)
    expect(s).toEqual({
      width: '160px',
      height: '90px',
      backgroundImage: 'url("/s/1.jpg")',
      backgroundSize: '1600px 900px',
      backgroundPosition: '-480px -180px',
    })
  })
})

describe('preloadSheets', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('loads the sheet playing and the next, once each, within the sheets there are', () => {
    const srcs: string[] = []
    vi.stubGlobal(
      'Image',
      class {
        decoding = ''
        set src(v: string) {
          srcs.push(v)
        }
      },
    )
    const p = { index: { ...ix, key: 'pre' }, sheetUrl: (n: number) => `/pre/${n}.jpg` }
    preloadSheets(p, 0)
    preloadSheets(p, 0)
    preloadSheets(p, 1)
    preloadSheets(p, 2) // the last: no next
    expect(srcs).toEqual(['/pre/0.jpg', '/pre/1.jpg', '/pre/2.jpg'])
  })
})
