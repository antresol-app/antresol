import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { SegmentedControl, type Segment } from '../SegmentedControl'

const SEGMENTS: Segment<string>[] = ['Всё', 'Фильмы', 'Сериалы', 'Музыка', 'Книги', 'Аудиокниги', 'Манга'].map((l) => ({ value: l, label: l }))

/** Gives the scroller a layout jsdom does not have. */
function layout(el: HTMLElement, { scrollWidth, clientWidth, scrollLeft = 0 }: { scrollWidth: number; clientWidth: number; scrollLeft?: number }) {
  Object.defineProperty(el, 'scrollWidth', { configurable: true, value: scrollWidth })
  Object.defineProperty(el, 'clientWidth', { configurable: true, value: clientWidth })
  el.scrollLeft = scrollLeft
}

/** The row's scroller: the element around the radio group. */
const rowOf = () => screen.getByRole('radiogroup').parentElement!

/**
 * A phone-wide layout for every element from the first render on (the row
 * measures in a layout effect): the row 375 wide over 600 of segments, each
 * segment 80 wide from x = 16.
 */
function phoneLayout() {
  const isRow = (el: Element) => el.firstElementChild?.getAttribute('role') === 'radiogroup'
  vi.spyOn(HTMLElement.prototype, 'scrollWidth', 'get').mockImplementation(function (this: HTMLElement) {
    return isRow(this) ? 600 : 0
  })
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function (this: HTMLElement) {
    return isRow(this) ? 375 : 0
  })
  vi.spyOn(Element.prototype, 'getBoundingClientRect').mockImplementation(function (this: Element) {
    if (isRow(this)) return DOMRect.fromRect({ x: 0, y: 0, width: 375, height: 56 })
    if (this.getAttribute('role') === 'radio') {
      const k = [...this.parentElement!.children].indexOf(this)
      return DOMRect.fromRect({ x: 16 + k * 80, y: 0, width: 80, height: 36 })
    }
    return DOMRect.fromRect({ x: 0, y: 0, width: 0, height: 0 })
  })
}

describe('SegmentedControl', () => {
  afterEach(() => vi.restoreAllMocks())

  it('arrow keys move the choice', () => {
    const onChange = vi.fn()
    render(<SegmentedControl label="Что искать" segments={SEGMENTS} value="Всё" onChange={onChange} />)
    fireEvent.keyDown(screen.getByRole('radiogroup'), { key: 'ArrowRight' })
    expect(onChange).toHaveBeenCalledWith('Фильмы')
    fireEvent.keyDown(screen.getByRole('radiogroup'), { key: 'ArrowLeft' })
    expect(onChange).toHaveBeenLastCalledWith('Манга')
  })

  it('a filter row fades the edge that has more segments past it', () => {
    render(<SegmentedControl label="Что искать" segments={SEGMENTS} value="Всё" onChange={() => {}} row />)
    const scroller = rowOf()
    // everything fits: no fade
    expect(scroller).not.toHaveAttribute('data-fade-start')
    expect(scroller).not.toHaveAttribute('data-fade-end')
    // too wide, at the start: only the end fades
    layout(scroller, { scrollWidth: 600, clientWidth: 375 })
    act(() => void fireEvent.scroll(scroller))
    expect(scroller).not.toHaveAttribute('data-fade-start')
    expect(scroller).toHaveAttribute('data-fade-end')
    // in the middle: both
    layout(scroller, { scrollWidth: 600, clientWidth: 375, scrollLeft: 100 })
    act(() => void fireEvent.scroll(scroller))
    expect(scroller).toHaveAttribute('data-fade-start')
    expect(scroller).toHaveAttribute('data-fade-end')
    // at the end: only the start
    layout(scroller, { scrollWidth: 600, clientWidth: 375, scrollLeft: 225 })
    act(() => void fireEvent.scroll(scroller))
    expect(scroller).toHaveAttribute('data-fade-start')
    expect(scroller).not.toHaveAttribute('data-fade-end')
  })

  it('a row too wide from the start fades its end before any scroll', () => {
    phoneLayout()
    render(<SegmentedControl label="Что искать" segments={SEGMENTS} value="Всё" onChange={() => {}} row />)
    expect(rowOf()).toHaveAttribute('data-fade-end')
    expect(rowOf()).not.toHaveAttribute('data-fade-start')
  })

  it('a row scrolls the chosen segment clear of the fade (a filter from the URL)', () => {
    phoneLayout()
    render(<SegmentedControl label="Что искать" segments={SEGMENTS} value="Манга" onChange={() => {}} row />)
    // «Манга» spans 496..576; the row is 375 wide, the fade a sixth of it
    expect(rowOf().scrollLeft).toBeCloseTo(576 - 375 + 375 / 6)
  })

  it('without `row` it is the bare control', () => {
    const { container } = render(<SegmentedControl label="Что искать" segments={SEGMENTS} value="Всё" onChange={() => {}} />)
    expect(container.firstElementChild).toBe(screen.getByRole('radiogroup'))
  })

  it('ships no test ids', () => {
    const { container } = render(<SegmentedControl label="Что искать" segments={SEGMENTS} value="Всё" onChange={() => {}} row />)
    expect(container.querySelector('[data-testid]')).toBeNull()
  })
})
