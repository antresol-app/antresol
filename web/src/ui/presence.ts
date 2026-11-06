// Keeps something mounted while it animates away (no animation library):
// `mounted` stays true for `exitMs` after `open` turns false, and `leaving`
// is true meanwhile, so its CSS can play the exit. With «Reduce motion» it
// goes at once.
import { useEffect, useState } from 'react'
import { useReducedMotion } from '../state/useMediaQuery'

export function usePresence(open: boolean, exitMs: number): { mounted: boolean; leaving: boolean } {
  const still = useReducedMotion()
  const [mounted, setMounted] = useState(open)
  useEffect(() => {
    if (open) return setMounted(true)
    if (still || exitMs <= 0) return setMounted(false)
    const id = setTimeout(() => setMounted(false), exitMs)
    return () => clearTimeout(id)
  }, [open, still, exitMs])
  const shown = open || mounted
  return { mounted: shown, leaving: shown && !open }
}
