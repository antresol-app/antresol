import { describe, expect, it } from 'vitest'
import { libraryLine } from '../Stats'

describe('Статистика', () => {
  it('names music, books and comics counts in Russian', () => {
    expect(libraryLine({ albums: 12, tracks: 140, books: 30, audiobooks: 4, comics: 2 })).toBe(
      'Музыка: 12 альбомов, 140 песен · Книги: 30 · Аудиокниги: 4 · Манга и комиксы: 2 серии',
    )
    expect(libraryLine({})).toBe('Музыка: 0 альбомов, 0 песен · Книги: 0 · Аудиокниги: 0 · Манга и комиксы: 0 серий')
  })
})
