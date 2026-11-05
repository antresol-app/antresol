// Stub of the sources build's module of the same name (without .stub), which
// is not in the public tree: imports resolve here only when it is absent
// (tsconfig moduleSuffixes, vite.config.ts resolve.extensions). Same exports
// and types; the core build never reaches them (__LIBRARY_EDITION__).
import type { ReactNode } from 'react'
import type { FollowRef, ItemDetail as Detail, KidRequestState, TitleInfo } from '../api/types'
import type { WatchWay } from '../components/WatchButton'

const absent = (): never => {
  throw new Error('not in this build')
}

export function fromTitle(_ti: TitleInfo, _kid: boolean): Detail {
  return absent()
}

export function followTarget(_it: Detail, _routeTmdb: number): { kind: 'series' | 'movie'; tmdbId: number } | null {
  return null
}

export function FollowButton(_props: { kind: 'series' | 'movie'; tmdbId: number; initial: FollowRef | null | undefined }) {
  return null
}

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

export function AddSeason(_props: { item: Detail; tmdbId: number; isAdmin: boolean }) {
  return null
}

export function TitleActions(_props: {
  item: Detail
  tmdbId: number
  seasons: TitleInfo['seasons']
  allowAll?: boolean
  isKid: boolean
  isAdmin: boolean
  profileId?: number
  canWatchOnline?: boolean
  follow?: ReactNode
  addSeason?: boolean
  kidRequest?: KidRequestState
}) {
  return null
}
