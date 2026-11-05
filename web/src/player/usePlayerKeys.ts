// Player keyboard shortcuts, attached once and always calling the latest
// handlers. Physical keys (e.code), so they work in the Russian layout too.
//   Space/K play-pause · ←/→ and J/L ±10 s · ↑/↓ volume · F fullscreen · M mute · Esc close
//   Enter/S the skip pill shown («Пропустить вступление», «Следующая серия»)
import { useEffect, useRef } from 'react'

export interface PlayerKeyHandlers {
  toggle: () => void
  skip: (sec: number) => void
  volume: (delta: number) => void
  fullscreen: () => void
  mute: () => void
  close: () => void
  /** Enter/S: the skip pill on screen; false when none shows (the key is not taken). */
  segment?: () => boolean
  /** Any handled key: show the controls. */
  poke: () => void
  /** A modal sheet is open over the player: every key belongs to it. */
  suspended?: boolean
}

const SKIP = 10

export function usePlayerKeys(handlers: PlayerKeyHandlers) {
  const ref = useRef(handlers)
  ref.current = handlers

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.altKey) return
      const t = e.target as HTMLElement | null
      const tag = t?.tagName
      if (t && (tag === 'TEXTAREA' || t.isContentEditable)) return
      if (tag === 'INPUT') {
        const type = (t as HTMLInputElement).type
        // A range input (volume) handles its own arrows; text fields keep every key.
        if (type !== 'range' || e.code.startsWith('Arrow')) return
      }
      const h = ref.current
      if (h.suspended) return
      let handled = true
      switch (e.code) {
        case 'Space':
        case 'KeyK':
          h.toggle()
          break
        case 'ArrowLeft':
        case 'KeyJ':
          h.skip(-SKIP)
          break
        case 'ArrowRight':
        case 'KeyL':
          h.skip(SKIP)
          break
        case 'ArrowUp':
          h.volume(0.1)
          break
        case 'ArrowDown':
          h.volume(-0.1)
          break
        case 'KeyF':
          h.fullscreen()
          break
        case 'KeyM':
          h.mute()
          break
        case 'Enter':
        case 'NumpadEnter':
          // a focused button takes its own Enter
          handled = tag !== 'BUTTON' && tag !== 'A' && (h.segment?.() ?? false)
          break
        case 'KeyS':
          handled = h.segment?.() ?? false
          break
        case 'Escape':
          // Esc leaves fullscreen first (Safari: webkitFullscreenElement)
          if (document.fullscreenElement || (document as Document & { webkitFullscreenElement?: Element | null }).webkitFullscreenElement) handled = false
          else h.close()
          break
        default:
          handled = false
      }
      if (handled) {
        e.preventDefault()
        h.poke()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
}
