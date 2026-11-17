// Stub of the sources build's module of the same name (without .stub), which
// is not in the public tree: imports resolve here only when it is absent
// (tsconfig moduleSuffixes, vite.config.ts resolve.extensions). Its online
// parts only, with the same types; the core build offers no «Онлайн» way.
import type { ReactNode } from 'react'
import type { TitleInfo } from '../api/types'
import type { WatchWay } from '../components/WatchButton'

interface OnlineSheetTarget {
  kind: string
  tmdbId: number
  title: string
  poster?: string
  seasons?: TitleInfo['seasons']
  canWatchOnline: boolean
  isKid: boolean
  openFromUrl?: boolean
  warm?: boolean
}

/** No «Онлайн» way: `children` gets none. */
export function OnlineWay({ children }: OnlineSheetTarget & { children: (ways: WatchWay[]) => ReactNode }) {
  return <>{children([])}</>
}
