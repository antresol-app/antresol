// While a sheet is open the page behind it must not scroll: a drag on the
// dimmed area (iOS scrolls the page under a fixed backdrop) or past the end
// of a short sheet's content moved the page. `inert` stops focus and
// clicks, not touch scrolling. Ref-counted like keyboard.ts: the page is
// given back what it had when the last user goes.
import { useEffect } from 'react'

let users = 0
let saved: { overflow: string; overscroll: string; bodyOverscroll: string } | null = null

function lock() {
  const html = document.documentElement.style
  const body = document.body.style
  saved = { overflow: html.overflow, overscroll: html.overscrollBehavior, bodyOverscroll: body.overscrollBehavior }
  html.overflow = 'hidden'
  html.overscrollBehavior = 'none'
  body.overscrollBehavior = 'none'
}

function unlock() {
  if (!saved) return
  const html = document.documentElement.style
  html.overflow = saved.overflow
  html.overscrollBehavior = saved.overscroll
  document.body.style.overscrollBehavior = saved.bodyOverscroll
  saved = null
}

/** Keeps the page from scrolling while the component is mounted. */
export function useScrollLock() {
  useEffect(() => {
    if (users++ === 0) lock()
    return () => {
      if (--users === 0) unlock()
    }
  }, [])
}
