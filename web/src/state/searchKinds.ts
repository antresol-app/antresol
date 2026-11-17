// What «Поиск» and the pages beside it share about result kinds: a kind's
// name and icon, a debounced value for a typed query, and the state of a
// search that loads (the addon SDK hands these to addons too).
import { useEffect, useState } from 'react'
import type { IconName } from '../components/Icon'
import { t as tr, type Key } from '../i18n'

/** A search's state: not asked yet, loading, failed with a text, or its items. */
export type Found<T> = { status: 'idle' } | { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; items: T }

const KIND: Record<string, Key> = {
  movie: 'common.kind.movie',
  series: 'common.kind.series',
  artist: 'common.kind.artist',
  album: 'common.kind.album',
  playlist: 'common.kind.playlist',
  music: 'common.kind.album',
  book: 'common.kind.book',
  audiobook: 'common.kind.audiobook',
  comic: 'common.kind.manga',
  comic_volume: 'common.kind.manga',
}
/** A kind's name in the interface language ('' for a kind it does not know). */
export const kindLabel = (kind: string) => (KIND[kind] ? tr(KIND[kind]) : '')

export const KIND_ICON: Record<string, IconName> = { movie: 'film', series: 'tv', music: 'music', book: 'book', audiobook: 'headphones', comic: 'book' }

/** `v`, once it has stayed the same for `ms`. */
export function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v)
  useEffect(() => {
    const id = window.setTimeout(() => setD(v), ms)
    return () => window.clearTimeout(id)
  }, [v, ms])
  return d
}
