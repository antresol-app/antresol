import { describe, expect, it } from 'vitest'
import type { FileEntry } from '../../../api/types'
import { clickSelect, emptySelection, fitMiddle, formatSize, kindLabel, moveLead, objects, selectAll, sortEntries } from '../model'
import { setLanguagePref } from '../../../i18n'

function e(name: string, over: Partial<FileEntry> = {}): FileEntry {
  return { name, path: name, isDir: false, size: 0, mtime: '2026-10-01T10:00:00Z', kind: 'other', ...over }
}

const names = (es: FileEntry[]) => es.map((x) => x.name)

describe('sortEntries', () => {
  const list = [
    e('Серия 10.mkv', { size: 30, kind: 'video', mtime: '2026-10-03T10:00:00Z' }),
    e('Season 01', { isDir: true, kind: 'folder' }),
    e('серия 2.mkv', { size: 10, kind: 'video', mtime: '2026-10-05T10:00:00Z' }),
    e('poster.jpg', { size: 20, kind: 'image', mtime: '2026-10-04T10:00:00Z' }),
    e('Extras', { isDir: true, kind: 'folder' }),
  ]

  it('keeps folders first and orders names naturally, ignoring case (Russian collation: Cyrillic first, as in Finder)', () => {
    expect(names(sortEntries(list, 'name', 'asc'))).toEqual(['Extras', 'Season 01', 'серия 2.mkv', 'Серия 10.mkv', 'poster.jpg'])
    expect(names(sortEntries(list, 'name', 'desc'))).toEqual(['Season 01', 'Extras', 'poster.jpg', 'Серия 10.mkv', 'серия 2.mkv'])
  })

  it('sorts by size, date and kind', () => {
    expect(names(sortEntries(list, 'size', 'desc')).slice(2)).toEqual(['Серия 10.mkv', 'poster.jpg', 'серия 2.mkv'])
    expect(names(sortEntries(list, 'mtime', 'desc')).slice(2)).toEqual(['серия 2.mkv', 'poster.jpg', 'Серия 10.mkv'])
    // By kind label: «Видео» before «Изображение»; ties by name.
    expect(names(sortEntries(list, 'kind', 'asc')).slice(2)).toEqual(['серия 2.mkv', 'Серия 10.mkv', 'poster.jpg'])
  })

  it('does not change the input', () => {
    const copy = [...list]
    sortEntries(list, 'size', 'asc')
    expect(list).toEqual(copy)
  })
})

describe('selection', () => {
  const order = ['a', 'b', 'c', 'd', 'e']

  it('a plain click selects one item', () => {
    const s = clickSelect(emptySelection, order, 'c', {})
    expect([...s.selected]).toEqual(['c'])
    expect(s.anchor).toBe('c')
    expect(s.lead).toBe('c')
  })

  it('⌘-click toggles items', () => {
    let s = clickSelect(emptySelection, order, 'b', {})
    s = clickSelect(s, order, 'd', { toggle: true })
    expect([...s.selected].sort()).toEqual(['b', 'd'])
    s = clickSelect(s, order, 'b', { toggle: true })
    expect([...s.selected]).toEqual(['d'])
  })

  it('Shift-click selects the range from the anchor, in both directions', () => {
    let s = clickSelect(emptySelection, order, 'b', {})
    s = clickSelect(s, order, 'd', { range: true })
    expect([...s.selected]).toEqual(['b', 'c', 'd'])
    expect(s.anchor).toBe('b')
    // A second Shift-click replaces the range, it does not add to it.
    s = clickSelect(s, order, 'a', { range: true })
    expect([...s.selected]).toEqual(['a', 'b'])
  })

  it('⌘-Shift-click adds a range to the existing selection', () => {
    let s = clickSelect(emptySelection, order, 'a', {})
    s = clickSelect(s, order, 'c', { toggle: true })
    s = clickSelect(s, order, 'e', { range: true, toggle: true })
    expect([...s.selected].sort()).toEqual(['a', 'c', 'd', 'e'])
  })

  it('arrow keys move the selection; with Shift they extend it', () => {
    let s = moveLead(emptySelection, order, 1, false)
    expect([...s.selected]).toEqual(['a'])
    s = moveLead(s, order, 1, false)
    expect([...s.selected]).toEqual(['b'])
    s = moveLead(s, order, 2, true)
    expect([...s.selected]).toEqual(['b', 'c', 'd'])
    s = moveLead(s, order, -3, true)
    expect([...s.selected]).toEqual(['a', 'b'])
    // Clamped at the ends.
    s = moveLead(s, order, 99, false)
    expect([...s.selected]).toEqual(['e'])
  })

  it('⌘A selects everything', () => {
    expect([...selectAll(order).selected]).toEqual(order)
  })
})

describe('formatting', () => {
  it('sizes use decimal units like Finder, with a Russian decimal comma', () => {
    expect(formatSize(0)).toBe('Ноль КБ')
    expect(formatSize(512)).toBe('512 байт')
    expect(formatSize(4096)).toBe('4 КБ')
    expect(formatSize(1_230_000_000)).toBe('1,23 ГБ')
    expect(formatSize(3_600_000_000_000)).toBe('3,6 ТБ')
  })

  it('kind labels', () => {
    expect(kindLabel(e('A', { isDir: true, kind: 'folder' }))).toBe('Папка')
    expect(kindLabel(e('x.mkv', { kind: 'video' }))).toBe('Видео MKV')
    expect(kindLabel(e('x.ru.srt', { kind: 'subtitle' }))).toBe('Субтитры SRT')
  })

  it('long names are truncated in the middle, keeping the extension', () => {
    const name = 'Interstellar.2014.1080p.BluRay.x264.DTS-HD.MA.5.1-GRP.mkv'
    const short = fitMiddle(name, 24)
    expect(short.length).toBeLessThanOrEqual(24)
    expect(short).toContain('…')
    expect(short.startsWith('Interstellar')).toBe(true)
    expect(short.endsWith('GRP.mkv')).toBe(true)
    expect(fitMiddle('Брат 2.mkv', 24)).toBe('Брат 2.mkv')
  })
})

describe('English labels', () => {
  it('formats sizes, kinds and counts in the interface language', () => {
    setLanguagePref('en')
    expect(formatSize(512)).toBe('512 bytes')
    expect(formatSize(1_230_000_000)).toBe('1.23 GB')
    expect(kindLabel(e('A', { isDir: true, kind: 'folder' }))).toBe('Folder')
    expect(kindLabel(e('x.mkv', { kind: 'video' }))).toBe('MKV video')
    expect(objects(1)).toBe('1 item')
    expect(objects(3)).toBe('3 items')
  })
})
