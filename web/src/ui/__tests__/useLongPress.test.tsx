import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useLongPress } from '../useLongPress'

function Probe({ onLong, onClick }: { onLong: (p: { x: number; y: number }) => void; onClick: () => void }) {
  const lp = useLongPress(onLong)
  return (
    <button data-testid="b" onClick={onClick} {...lp}>
      x
    </button>
  )
}

const touch = (x = 5, y = 6) => ({ pointerType: 'touch', clientX: x, clientY: y })

describe('useLongPress', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('long press fires after 480 ms', () => {
    const onLong = vi.fn()
    render(<Probe onLong={onLong} onClick={() => undefined} />)
    fireEvent.pointerDown(screen.getByTestId('b'), touch(5, 6))
    act(() => void vi.advanceTimersByTime(479))
    expect(onLong).not.toHaveBeenCalled()
    act(() => void vi.advanceTimersByTime(2))
    expect(onLong).toHaveBeenCalledWith({ x: 5, y: 6 })
  })
  it('long press cancels on move', () => {
    const onLong = vi.fn()
    render(<Probe onLong={onLong} onClick={() => undefined} />)
    const b = screen.getByTestId('b')
    fireEvent.pointerDown(b, touch(5, 6))
    fireEvent.pointerMove(b, touch(30, 6))
    act(() => void vi.advanceTimersByTime(600))
    expect(onLong).not.toHaveBeenCalled()
  })
  it('long press swallows the click', () => {
    const onClick = vi.fn()
    render(<Probe onLong={() => undefined} onClick={onClick} />)
    const b = screen.getByTestId('b')
    fireEvent.pointerDown(b, touch())
    act(() => void vi.advanceTimersByTime(500))
    fireEvent.pointerUp(b, touch())
    fireEvent.click(b)
    expect(onClick).not.toHaveBeenCalled()
    act(() => void vi.advanceTimersByTime(500))
    fireEvent.click(b)
    expect(onClick).toHaveBeenCalledTimes(1)
  })
  it('mouse right-click opens at point', () => {
    const onLong = vi.fn()
    render(<Probe onLong={onLong} onClick={() => undefined} />)
    const b = screen.getByTestId('b')
    const notPrevented = fireEvent.contextMenu(b, { clientX: 11, clientY: 12 })
    expect(onLong).toHaveBeenCalledWith({ x: 11, y: 12 })
    expect(notPrevented).toBe(false)
  })
  it('contextmenu after the timer fired does not run onLong twice', () => {
    const onLong = vi.fn()
    render(<Probe onLong={onLong} onClick={() => undefined} />)
    const b = screen.getByTestId('b')
    fireEvent.pointerDown(b, touch())
    act(() => void vi.advanceTimersByTime(500))
    const notPrevented = fireEvent.contextMenu(b, { clientX: 5, clientY: 6 })
    expect(onLong).toHaveBeenCalledTimes(1)
    expect(notPrevented).toBe(false)
  })
  it('mouse press does not start the timer', () => {
    const onLong = vi.fn()
    render(<Probe onLong={onLong} onClick={() => undefined} />)
    fireEvent.pointerDown(screen.getByTestId('b'), { pointerType: 'mouse', clientX: 1, clientY: 1 })
    act(() => void vi.advanceTimersByTime(600))
    expect(onLong).not.toHaveBeenCalled()
  })
  it('short tap is a click', () => {
    const onLong = vi.fn()
    const onClick = vi.fn()
    render(<Probe onLong={onLong} onClick={onClick} />)
    const b = screen.getByTestId('b')
    fireEvent.pointerDown(b, touch())
    act(() => void vi.advanceTimersByTime(100))
    fireEvent.pointerUp(b, touch())
    fireEvent.click(b)
    act(() => void vi.advanceTimersByTime(1000))
    expect(onLong).not.toHaveBeenCalled()
    expect(onClick).toHaveBeenCalledTimes(1)
  })
})
