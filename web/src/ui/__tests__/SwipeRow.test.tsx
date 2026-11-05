import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { SwipeRow } from '../SwipeRow'

function setup(onDelete = vi.fn(), onTap = vi.fn()) {
  render(
    <ul>
      <SwipeRow
        actions={[
          { key: 'pause', label: 'Пауза', icon: 'pause', onSelect: () => {} },
          { key: 'delete', label: 'Удалить', icon: 'trash', tone: 'destructive', onSelect: onDelete },
        ]}
      >
        <button type="button" onClick={onTap}>
          Интерстеллар
        </button>
      </SwipeRow>
    </ul>,
  )
  const row = screen.getByRole('listitem')
  const content = screen.getByText('Интерстеллар').parentElement!
  return { row, content, onDelete, onTap }
}

const swipe = (el: Element, from: number, to: number, pointerType = 'touch', dy = 0) => {
  fireEvent.pointerDown(el, { pointerId: 1, pointerType, clientX: from, clientY: 100, isPrimary: true })
  fireEvent.pointerMove(el, { pointerId: 1, pointerType, clientX: (from + to) / 2, clientY: 100 + dy / 2 })
  fireEvent.pointerMove(el, { pointerId: 1, pointerType, clientX: to, clientY: 100 + dy })
  fireEvent.pointerUp(el, { pointerId: 1, pointerType, clientX: to, clientY: 100 + dy })
}

/** Events `step` ms apart (an event's timeStamp is read from Date.now() here): how fast the finger moved */
const clock = (step: number) => {
  let now = 1000
  vi.spyOn(Date, 'now').mockImplementation(() => (now += step))
}

const tap = (el: Element) => {
  fireEvent.pointerDown(el, { pointerId: 3, pointerType: 'touch', clientX: 200, clientY: 100 })
  fireEvent.pointerUp(el, { pointerId: 3, pointerType: 'touch', clientX: 200, clientY: 100 })
  fireEvent.click(el)
}

describe('SwipeRow', () => {
  it('a swipe to the left reveals the row’s actions; closed, they are out of reach (the «⋯» menu has them)', () => {
    const { row, content } = setup()
    expect(row).toHaveAttribute('data-open', 'false')
    expect(screen.queryByRole('button', { name: 'Удалить' })).not.toBeInTheDocument()
    swipe(content, 300, 120)
    expect(row).toHaveAttribute('data-open', 'true')
    expect(screen.getByRole('button', { name: 'Удалить' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Пауза' })).toBeInTheDocument()
  })

  it('a short, slow swipe springs back closed', () => {
    clock(60)
    const { row, content } = setup()
    swipe(content, 300, 270)
    expect(row).toHaveAttribute('data-open', 'false')
  })

  it('a quick flick opens it though it went less than half the way (velocity, as on iOS)', () => {
    clock(5)
    const { row, content } = setup()
    swipe(content, 300, 270)
    expect(row).toHaveAttribute('data-open', 'true')
  })

  it('one big move and the release, with no frame between them, still opens it', () => {
    clock(200)
    const { row, content } = setup()
    fireEvent.pointerDown(content, { pointerId: 1, pointerType: 'touch', clientX: 300, clientY: 100 })
    fireEvent.pointerMove(content, { pointerId: 1, pointerType: 'touch', clientX: 120, clientY: 100 })
    fireEvent.pointerUp(content, { pointerId: 1, pointerType: 'touch', clientX: 120, clientY: 100 })
    expect(row).toHaveAttribute('data-open', 'true')
  })

  it('scrolling the list from an open row closes it', () => {
    const { row, content } = setup()
    swipe(content, 300, 120)
    expect(row).toHaveAttribute('data-open', 'true')
    swipe(content, 200, 200, 'touch', -150)
    expect(row).toHaveAttribute('data-open', 'false')
  })

  it('a vertical drag is the page scrolling, not a swipe', () => {
    const { row, content } = setup()
    swipe(content, 300, 230, 'touch', 200)
    expect(row).toHaveAttribute('data-open', 'false')
  })

  it('a mouse never swipes (the pointer has the «⋯» menu)', () => {
    const { row, content } = setup()
    swipe(content, 300, 100, 'mouse')
    expect(row).toHaveAttribute('data-open', 'false')
  })

  it('the swipe does not click the row; a tap on an open row closes it instead of acting', () => {
    const { row, content, onTap } = setup()
    swipe(content, 300, 120)
    // a click the browser may send at the end of the swipe
    fireEvent.click(screen.getByText('Интерстеллар'))
    expect(onTap).not.toHaveBeenCalled()
    expect(row).toHaveAttribute('data-open', 'true')
    tap(screen.getByText('Интерстеллар'))
    expect(onTap).not.toHaveBeenCalled()
    expect(row).toHaveAttribute('data-open', 'false')
    tap(screen.getByText('Интерстеллар'))
    expect(onTap).toHaveBeenCalledTimes(1)
  })

  it('an action runs and closes the row', () => {
    const { row, content, onDelete } = setup()
    swipe(content, 300, 120)
    fireEvent.click(screen.getByRole('button', { name: 'Удалить' }))
    expect(onDelete).toHaveBeenCalledTimes(1)
    expect(row).toHaveAttribute('data-open', 'false')
  })

  it('a touch elsewhere on the page closes an open row', () => {
    const { row, content } = setup()
    swipe(content, 300, 120)
    fireEvent.pointerDown(document.body, { pointerId: 2, pointerType: 'touch' })
    expect(row).toHaveAttribute('data-open', 'false')
  })
})
