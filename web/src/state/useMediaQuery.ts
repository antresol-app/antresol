import { useSyncExternalStore } from 'react'

/** Live result of a CSS media query. */
export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (onChange) => {
      const mql = window.matchMedia(query)
      mql.addEventListener('change', onChange)
      return () => mql.removeEventListener('change', onChange)
    },
    () => window.matchMedia(query).matches,
    () => false,
  )
}

/** Phone-sized layout: bottom tab bar instead of the top pill. */
export const COMPACT_QUERY = '(max-width: 699px)'
export const useIsCompact = () => useMediaQuery(COMPACT_QUERY)
/** A short window (a phone on its side): the layouts tighten vertically. */
export const SHORT_QUERY = '(max-height: 500px)'
export const useReducedMotion = () => useMediaQuery('(prefers-reduced-motion: reduce)')
