// The on-screen keyboard's height, as the --keyboard-inset custom property
// on <html>. iOS Safari lays a fixed bottom sheet out against the layout
// viewport, so the keyboard simply covers its fields and buttons; the
// visual viewport is what is still visible. A sheet on a phone lifts itself
// by this inset (Sheet.module.css), so what is typed stays in view.
import { useEffect } from 'react'

export interface ViewportLike {
  height: number
  offsetTop: number
}

/** How much of the window's bottom the keyboard covers (0 without one). */
export function keyboardInset(windowHeight: number, vv: ViewportLike | null | undefined): number {
  if (!vv) return 0
  const covered = windowHeight - vv.height - vv.offsetTop
  // a toolbar sliding away or pinch zoom moves a few pixels: not a keyboard
  return covered > 80 ? Math.round(covered) : 0
}

/** Inputs that take no text: focusing one raises no keyboard. */
const NO_TEXT = ['checkbox', 'radio', 'button', 'submit', 'reset', 'range', 'file', 'color', 'image', 'hidden']

/** Would focusing `el` open a text keyboard (a text field, a text area, an editable element)? */
export function takesText(el: Element | null | undefined): boolean {
  if (!el) return false
  if (el instanceof HTMLInputElement) return !NO_TEXT.includes(el.type)
  return el instanceof HTMLTextAreaElement || (el instanceof HTMLElement && el.isContentEditable)
}

/**
 * The primary pointer is a finger: this device raises its on-screen keyboard
 * (and, on iOS, the AutoFill bar) for any text field that takes focus. A
 * script that focuses a field there is not a convenience (as with a mouse and
 * a hardware keyboard, where typing can start at once) but a keyboard that
 * covers half the screen before the person chose to type.
 */
export function touchPrimary(): boolean {
  return typeof window !== 'undefined' && !!window.matchMedia?.('(pointer: coarse)').matches
}

let users = 0
let detach: (() => void) | null = null

function attach() {
  const vv = window.visualViewport
  const root = document.documentElement
  const update = () => {
    const vv = window.visualViewport
    const kb = keyboardInset(window.innerHeight, vv)
    root.style.setProperty('--keyboard-inset', `${kb}px`)
    // with the keyboard up iOS may scroll the page up to show the field:
    // what is above the visible part is out of view as well
    root.style.setProperty('--vv-top', `${kb > 0 ? Math.max(0, Math.round(vv?.offsetTop ?? 0)) : 0}px`)
  }
  update()
  vv?.addEventListener('resize', update)
  vv?.addEventListener('scroll', update)
  window.addEventListener('resize', update)
  return () => {
    vv?.removeEventListener('resize', update)
    vv?.removeEventListener('scroll', update)
    window.removeEventListener('resize', update)
    root.style.removeProperty('--keyboard-inset')
    root.style.removeProperty('--vv-top')
  }
}

/** Keeps --keyboard-inset up to date while a component that needs it is mounted. */
export function useKeyboardInset() {
  useEffect(() => {
    if (users++ === 0) detach = attach()
    return () => {
      if (--users === 0) {
        detach?.()
        detach = null
      }
    }
  }, [])
}
