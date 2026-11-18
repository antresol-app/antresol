import type { IconName } from '../components/Icon'
import { t, type Key } from '../i18n'

export interface Section {
  to: string
  /** The catalog key of the label. */
  labelKey: Key
  /** The label in the interface language (read at render time). */
  readonly label: string
  icon: IconName
  /** Show only the icon in the top pill (label is still the accessible name). */
  iconOnly?: boolean
}

/** Top-level sections, in navigation order (variant B). */
const section = (to: string, labelKey: Key, icon: IconName, iconOnly?: boolean): Section => ({
  to,
  labelKey,
  icon,
  ...(iconOnly ? { iconOnly } : {}),
  get label() {
    return t(this.labelKey)
  },
})

export const SECTIONS: Section[] = [
  section('/search', 'common.search', 'search', true),
  section('/', 'common.home', 'home'),
  section('/movies', 'common.movies', 'film'),
  section('/series', 'common.series', 'tv'),
  section('/tv', 'common.tv', 'broadcast'),
  section('/music', 'common.music', 'music'),
  section('/books', 'common.books', 'book'),
]

/** A tab of the phone's tab bar. */
export interface PhoneTab {
  to: string
  labelKey: Key
  readonly label: string
  icon: IconName
  /** The sections whose pages make this tab current (the home tab: only "/" itself). */
  roots: string[]
}

/** The sections «Медиа» holds on a phone, in its switch's order. */
export const MEDIA_ROOTS = ['/music', '/books'] as const
export type MediaRoot = (typeof MEDIA_ROOTS)[number]

const tab = (to: string, labelKey: Key, icon: IconName, roots: string[] = [to]): PhoneTab => ({
  to,
  labelKey,
  icon,
  roots,
  get label() {
    return t(this.labelKey)
  },
})

/**
 * The phone's tabs: an iPhone has room for five (HIG), the top pill for all
 * seven sections, so on a phone «Медиа» holds Музыка and Книги (books,
 * audiobooks, comics) and leads to whichever of them was open last
 * (`media`); /music and /books themselves stay where they are.
 */
export function phoneTabs(media: MediaRoot): PhoneTab[] {
  return [
    tab('/', 'common.home', 'homeFill'),
    tab('/movies', 'common.movies', 'filmFill'),
    tab('/series', 'common.series', 'tvFill'),
    tab('/tv', 'common.tv', 'antennaRadiowaves'),
    tab(media, 'common.media', 'squareStackFill', [...MEDIA_ROOTS]),
  ]
}

/** Which section a path belongs to ("/series/42" → "/series"). */
export function activeSection(pathname: string): string {
  if (pathname === '/' || pathname === '') return '/'
  const hit = SECTIONS.find((s) => s.to !== '/' && (pathname === s.to || pathname.startsWith(s.to + '/')))
  return hit ? hit.to : ''
}

/** What «Поиск» looks through: everything, or one kind — the library and «Найти и добавить» alike. */
export type SearchScope = 'all' | 'movies' | 'series' | 'music' | 'books' | 'audiobooks' | 'comics'

export const SEARCH_SCOPES: SearchScope[] = ['all', 'movies', 'series', 'music', 'books', 'audiobooks', 'comics']

// «Книги» holds books, audiobooks and manga, three filters of the search:
// searching from it keeps all of them (no filter)
const SCOPE_OF_SECTION: Record<string, SearchScope> = { '/movies': 'movies', '/series': 'series', '/music': 'music' }

/** The search scope of the section a path belongs to ("/music/album/3" → "music"; «Книги» → everything). */
export function scopeOf(pathname: string): SearchScope {
  return SCOPE_OF_SECTION[activeSection(pathname)] ?? 'all'
}

/** Where the search button leads from a path: the section's own scope, or the whole library. */
export function searchHref(pathname: string): string {
  if (pathname === '/search') return '/search'
  const scope = scopeOf(pathname)
  return scope === 'all' ? '/search' : `/search?scope=${scope}`
}
