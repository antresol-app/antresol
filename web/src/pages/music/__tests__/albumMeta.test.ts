import { describe, expect, it } from 'vitest'
import type { AlbumDetail } from '../../../api/types'
import { albumMeta } from '../Album'

function album(year: number, genres = ['Rock']): AlbumDetail {
  return { year, genres, quality: 'Lossless · FLAC' } as AlbumDetail
}

describe('albumMeta', () => {
  it('shows genre, year and quality', () => {
    expect(albumMeta(album(1973))).toBe('Rock · 1973 · Lossless')
  })
  it('hides implausible years', () => {
    for (const y of [0, 1, 999, 3000, 20240, -1, NaN]) {
      expect(albumMeta(album(y))).toBe('Rock · Lossless')
    }
  })
  it('keeps the range boundaries', () => {
    expect(albumMeta(album(1000))).toBe('Rock · 1000 · Lossless')
    expect(albumMeta(album(2999))).toBe('Rock · 2999 · Lossless')
  })
  it('works without a genre', () => {
    expect(albumMeta(album(1, []))).toBe('Lossless')
  })
})
