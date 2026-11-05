// The server's source offer (GET /api/about, AGPL §13), asked once per page
// load: the sign-in screens and «О сервере» show the link only when the
// server gives one.
import { useEffect, useState } from 'react'
import { getAbout } from '../api/client'
import type { About } from '../api/types'

let pending: Promise<About | null> | null = null

/** The answer, shared by every caller; a failed request is asked again next time. */
export function loadAbout(): Promise<About | null> {
  pending ??= getAbout().catch(() => {
    pending = null
    return null
  })
  return pending
}

/** For tests: forget the answer. */
export function resetAbout() {
  pending = null
}

/** The link to show: an absolute http(s) URL, else '' (no link). */
export function sourceLink(about: About | null | undefined): string {
  const u = typeof about?.sourceUrl === 'string' ? about.sourceUrl : ''
  return /^https?:\/\/[^\s]+$/i.test(u) ? u : ''
}

/** GET /api/about, null until it answers (or when it fails). */
export function useAbout(): About | null {
  const [about, setAbout] = useState<About | null>(null)
  useEffect(() => {
    let live = true
    void loadAbout().then((a) => {
      if (live) setAbout(a)
    })
    return () => {
      live = false
    }
  }, [])
  return about
}
