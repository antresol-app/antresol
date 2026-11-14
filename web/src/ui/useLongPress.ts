// Phone: a long press opens a context menu (iOS has no right-click); a mouse
// right-click does the same at the pointer. Spread the result on an element.
import { useMemo, useRef } from 'react'
import type { MouseEvent as RMouseEvent, PointerEvent as RPointerEvent } from 'react'

export type PressPoint = { x: number; y: number }

export function useLongPress(onLong: (pt: PressPoint) => void, opts: { ms?: number } = {}) {
  const latest = useRef({ onLong, ms: opts.ms ?? 480 })
  latest.current = { onLong, ms: opts.ms ?? 480 }
  const press = useRef<{ timer: ReturnType<typeof setTimeout>; x: number; y: number; fired: boolean } | null>(null)
  return useMemo(() => {
    const cancel = () => {
      const s = press.current
      if (!s) return
      if (!s.fired) {
        clearTimeout(s.timer)
        press.current = null
      } else {
        // Swallow only the click that ends this press, if the browser sends one.
        setTimeout(() => {
          if (press.current === s) press.current = null
        }, 400)
      }
    }
    return {
      onPointerDown: (e: RPointerEvent) => {
        if (e.pointerType === 'mouse') return
        const x = e.clientX
        const y = e.clientY
        if (press.current) clearTimeout(press.current.timer)
        const s = {
          x,
          y,
          fired: false,
          timer: setTimeout(() => {
            if (press.current !== s) return
            s.fired = true
            navigator.vibrate?.(8)
            latest.current.onLong({ x, y })
          }, latest.current.ms),
        }
        press.current = s
      },
      onPointerMove: (e: RPointerEvent) => {
        const s = press.current
        if (s && !s.fired && Math.hypot(e.clientX - s.x, e.clientY - s.y) > 10) cancel()
      },
      onPointerUp: cancel,
      onPointerCancel: cancel,
      // The tap that ends a long press must not also activate the element.
      onClickCapture: (e: RMouseEvent) => {
        if (press.current?.fired) {
          e.stopPropagation()
          e.preventDefault()
          press.current = null
        }
      },
      onContextMenu: (e: RMouseEvent) => {
        e.preventDefault()
        // Android sends contextmenu after the timer already opened the menu.
        if (press.current?.fired) return
        latest.current.onLong({ x: e.clientX, y: e.clientY })
      },
    }
  }, [])
}
