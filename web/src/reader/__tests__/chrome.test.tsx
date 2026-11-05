import { act, fireEvent, render, screen } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { chromeKey, HIDE_AFTER_MS, ReaderChrome, useAutoHide } from '../ReaderChrome'

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

/** The panels the way the readers use them: keys on the window. */
function Harness({ onBack }: { onBack: () => void }) {
  const [locked, setLocked] = useState(false)
  const chrome = useAutoHide(locked)
  return (
    <div
      tabIndex={-1}
      data-testid="page"
      onKeyDown={(e) => {
        if (e.key !== 'ArrowRight') chromeKey(e.nativeEvent, chrome, onBack)
        else chrome.hide()
      }}
    >
      <ReaderChrome title="Книга" visible={chrome.visible} theme="night" onBack={onBack} chapter="" fraction={null} onLockChange={setLocked} />
    </div>
  )
}

const hidden = () => document.querySelector('[data-reader-chrome]')!.hasAttribute('data-hidden')

describe('reader panels and the keyboard', () => {
  it('a key shows them, focus keeps them, Esc shows them and then leaves', () => {
    const onBack = vi.fn()
    render(<Harness onBack={onBack} />)
    const page = screen.getByTestId('page')
    act(() => void vi.advanceTimersByTime(HIDE_AFTER_MS))
    expect(hidden()).toBe(true)
    // hidden panels stay in the tab order (not inert)
    expect(screen.getByRole('button', { name: 'Книги' }).closest('[inert]')).toBeNull()

    fireEvent.keyDown(page, { key: 'Escape' })
    expect(hidden()).toBe(false)
    expect(onBack).not.toHaveBeenCalled()
    act(() => void vi.advanceTimersByTime(HIDE_AFTER_MS))
    expect(hidden()).toBe(true)

    fireEvent.keyDown(page, { key: 'm' })
    expect(hidden()).toBe(false)
    act(() => screen.getByRole('button', { name: 'Книги' }).focus())
    act(() => void vi.advanceTimersByTime(HIDE_AFTER_MS * 2))
    expect(hidden()).toBe(false)
    fireEvent.keyDown(page, { key: 'ArrowRight' }) // a page turn does not hide them while focus is there
    expect(hidden()).toBe(false)

    fireEvent.keyDown(page, { key: 'Escape' })
    expect(onBack).toHaveBeenCalledTimes(1)

    // focus gone: they hide again after the delay
    act(() => page.focus())
    act(() => void vi.advanceTimersByTime(HIDE_AFTER_MS))
    expect(hidden()).toBe(true)
  })
})
