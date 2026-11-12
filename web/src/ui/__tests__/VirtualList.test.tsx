import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { FilterChips } from '../FilterChips'
import { VirtualList, visibleRange } from '../VirtualList'

describe('visibleRange', () => {
  it('covers the band on screen plus the overscan, clamped to the list', () => {
    expect(visibleRange(0, 800, 50, 3000, 5)).toEqual([0, 21])
    expect(visibleRange(-1000, 800, 50, 3000, 5)).toEqual([15, 41])
    expect(visibleRange(-200_000, 800, 50, 3000, 5)).toEqual([3000, 3000])
    expect(visibleRange(900, 800, 50, 3000, 5)).toEqual([0, 3]) // just below the screen: the overscan only
    expect(visibleRange(0, 800, 50, 0, 5)).toEqual([0, 0])
  })
})

describe('VirtualList', () => {
  it('renders a few rows of thousands and keeps the full height', () => {
    const { container } = render(<VirtualList count={3000} itemHeight={52} renderItem={(i) => <span>Канал {i + 1}</span>} />)
    const list = container.firstElementChild as HTMLElement
    expect(list.style.height).toBe(`${3000 * 52}px`)
    const rows = screen.getAllByRole('listitem')
    expect(rows.length).toBeGreaterThan(0)
    expect(rows.length).toBeLessThan(100)
    expect(rows[1]!.getAttribute('aria-posinset')).toBe('2')
    expect(rows[1]!.getAttribute('aria-setsize')).toBe('3000')
    expect(rows[1]!.style.transform).toBe('translateY(52px)')
  })

  it('marks rows of a table with their row index (the header is row 1)', () => {
    render(<VirtualList role="rowgroup" itemRole="row" count={10} itemHeight={40} renderItem={(i) => <span>{i}</span>} />)
    expect(screen.getAllByRole('row')[0]!.getAttribute('aria-rowindex')).toBe('2')
  })
})

describe('FilterChips', () => {
  it('is a radio group with counts; arrows move the choice', () => {
    const picked: string[] = []
    render(<FilterChips label="Группы" value="" onChange={(v) => picked.push(v)} chips={[{ value: '', label: 'Все', count: 3 }, { value: 'a', label: 'Новости', count: 2 }, { value: 'b', label: 'Спорт', count: 1 }]} />)
    const radios = screen.getAllByRole('radio')
    expect(radios[0]).toHaveAttribute('aria-checked', 'true')
    expect(radios[1]).toHaveTextContent('Новости2')
    fireEvent.keyDown(radios[0]!, { key: 'ArrowRight' })
    fireEvent.keyDown(radios[0]!, { key: 'End' })
    fireEvent.click(radios[1]!)
    expect(picked).toEqual(['a', 'b', 'a'])
  })
})

describe('VirtualList shrinking', () => {
  it('never renders an index past a count that just got smaller', () => {
    const seen: number[] = []
    const { rerender } = render(<VirtualList count={3000} itemHeight={52} renderItem={(i) => <span>{i}</span>} />)
    rerender(<VirtualList count={2} itemHeight={52} renderItem={(i) => (seen.push(i), <span>{i}</span>)} />)
    expect(Math.max(...seen)).toBeLessThan(2)
  })
})
