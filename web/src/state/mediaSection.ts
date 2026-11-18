// Which part of «Медиа» (the phone's tab of Музыка and Книги) was open last,
// so the tab leads back to it, as an iPhone's tab keeps its place. A
// per-device convenience: plain localStorage, which may be missing or throw
// (a private window), and then the tab leads to Музыка.
import { MEDIA_ROOTS, type MediaRoot } from '../app/sections'

const KEY = 'vault.mediaSection'

/** The media section a path is in ("/books/12" → "/books"), or none. */
export function mediaRootOf(pathname: string): MediaRoot | undefined {
  return MEDIA_ROOTS.find((r) => pathname === r || pathname.startsWith(r + '/'))
}

export function lastMediaRoot(): MediaRoot {
  try {
    const v = localStorage.getItem(KEY)
    return MEDIA_ROOTS.find((r) => r === v) ?? MEDIA_ROOTS[0]
  } catch {
    return MEDIA_ROOTS[0]
  }
}

/** Remembers the media section of a path (other paths change nothing). */
export function rememberMediaRoot(pathname: string): void {
  const root = mediaRootOf(pathname)
  if (!root) return
  try {
    localStorage.setItem(KEY, root)
  } catch {
    // no storage: the tab leads to Музыка
  }
}
