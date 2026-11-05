import { afterEach, describe, expect, it } from 'vitest'
import { act, render } from '@testing-library/react'
import { keyboardInset, useKeyboardInset } from '../keyboard'

class FakeViewport extends EventTarget {
  height = 844
  offsetTop = 0
}

function Uses() {
  useKeyboardInset()
  return null
}

const inset = () => document.documentElement.style.getPropertyValue('--keyboard-inset')

describe('keyboardInset', () => {
  it('is what the visual viewport no longer shows of the window', () => {
    expect(keyboardInset(844, { height: 508, offsetTop: 0 })).toBe(336)
    // scrolled while the keyboard is up: the visible part moved down
    expect(keyboardInset(844, { height: 508, offsetTop: 40 })).toBe(296)
  })

  it('is zero without a keyboard, a visual viewport, or for a toolbar’s few pixels', () => {
    expect(keyboardInset(844, { height: 844, offsetTop: 0 })).toBe(0)
    expect(keyboardInset(844, null)).toBe(0)
    expect(keyboardInset(844, { height: 800, offsetTop: 0 })).toBe(0)
  })
})

describe('useKeyboardInset', () => {
  const original = window.visualViewport
  afterEach(() => Object.defineProperty(window, 'visualViewport', { configurable: true, value: original }))

  it('keeps --keyboard-inset on <html> current while mounted and clears it after the last user', () => {
    const vv = new FakeViewport()
    Object.defineProperty(window, 'visualViewport', { configurable: true, value: vv })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 844 })
    const a = render(<Uses />)
    const b = render(<Uses />)
    expect(inset()).toBe('0px')
    act(() => {
      vv.height = 500
      vv.dispatchEvent(new Event('resize'))
    })
    expect(inset()).toBe('344px')
    // iOS scrolled the page up to show the field: the part above is out of view too
    act(() => {
      vv.offsetTop = 40
      vv.height = 460
      vv.dispatchEvent(new Event('scroll'))
    })
    expect(document.documentElement.style.getPropertyValue('--vv-top')).toBe('40px')
    a.unmount()
    expect(inset()).toBe('344px') // the other user still needs it
    b.unmount()
    expect(inset()).toBe('')
    expect(document.documentElement.style.getPropertyValue('--vv-top')).toBe('')
  })
})
