import { describe, expect, it } from 'vitest'
import { ADDON_COLORS, segmentsOf } from '../DiskMeter'

describe('segmentsOf', () => {
  it('addon categories use their labels and the rotation', () => {
    const segs = segmentsOf([
      { id: 'movies', bytes: 5 },
      { id: 'addon:dl:data', bytes: 4, label: 'Загрузки' },
      { id: 'addon:dl:cache', bytes: 3, label: 'Кеш просмотра' },
      { id: 'addon:hello:a', bytes: 0, label: 'Пусто' },
      { id: 'addon:hello:b', bytes: 2, label: 'Б' },
      { id: 'addon:hello:c', bytes: 2, label: 'В' },
      { id: 'addon:hello:d', bytes: 1 },
      { id: 'other', bytes: 9 },
    ])
    expect(segs.map((s) => [s.id, s.label, s.color])).toEqual([
      ['movies', 'Фильмы', 'var(--blue)'],
      ['addon:dl:data', 'Загрузки', ADDON_COLORS[0]],
      ['addon:dl:cache', 'Кеш просмотра', ADDON_COLORS[1]],
      ['addon:hello:b', 'Б', ADDON_COLORS[2]],
      ['addon:hello:c', 'В', ADDON_COLORS[3]],
      // the rotation starts over; without a label, the id
      ['addon:hello:d', 'addon:hello:d', ADDON_COLORS[0]],
      ['other', 'Другое', 'var(--gray)'],
    ])
    expect(ADDON_COLORS).toEqual(['var(--green)', 'var(--teal)', 'var(--indigo)', 'var(--yellow)'])
  })

  it('the core categories keep their own names and colours', () => {
    expect(segmentsOf([{ id: 'books', bytes: 1 }])).toEqual([{ id: 'books', bytes: 1, label: 'Книги', color: 'var(--orange)' }])
  })
})
