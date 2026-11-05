// The sort / filter menu of a list page («Загрузки», «Управление → Файлы»):
// a round glass button next to the section switcher that opens a menu of
// radio groups (a heading each, separated), a blue dot on the button while a
// filter is on and «Сбросить фильтры» at the end. The view lives in the URL
// (useUrlView): defaults are left out, unknown values fall back to them.
import { Fragment, type ReactNode } from 'react'
import { useSearchParams } from 'react-router'
import { useT, type Key } from '../i18n'
import { IconButton } from '../ui/IconButton'
import type { IconName } from './Icon'
import { MenuHeading, MenuItem, MenuSeparator, Menu } from '../ui/Menu'
import styles from './ViewMenu.module.css'

export interface ViewOption<T extends string = string> {
  value: T
  label: string
  detail?: string
}

/** One radio group of the menu. */
export interface ViewGroup<T extends string = string> {
  heading: string
  options: ViewOption<T>[]
  value: T
  onChange(v: T): void
  /** Left out of the menu (e.g. a filter nothing in the section can pass). */
  hidden?: boolean
}

export interface ViewMenuProps {
  /** The button's and the menu's accessible name. */
  label: string
  groups: ViewGroup[]
  /** A filter is on: the dot, and «reset» at the end. */
  filtered: boolean
  resetLabel: string
  onReset: () => void
}

export function ViewMenu({ label, groups, filtered, resetLabel, onReset }: ViewMenuProps) {
  const shown = groups.filter((g) => !g.hidden)
  return (
    <Menu
      label={label}
      placement="bottom-start"
      // sort and filters at once: taller than a usual menu (still cut to the window)
      maxHeight={720}
      trigger={(p) => (
        <span className={styles.trigger}>
          <IconButton {...p} icon="sliders" label={label} />
          {filtered && <span className={styles.dot} aria-hidden data-testid="filters-on" />}
        </span>
      )}
    >
      {(close) => (
        <>
          {shown.map((g, i) => (
            <Fragment key={g.heading}>
              {i > 0 && <MenuSeparator />}
              <MenuHeading>{g.heading}</MenuHeading>
              {g.options.map((o) => (
                <MenuItem key={o.value} label={o.label} detail={o.detail} checked={g.value === o.value} onSelect={() => (g.onChange(o.value), close())} />
              ))}
            </Fragment>
          ))}
          {filtered && (
            <>
              <MenuSeparator />
              <MenuItem label={resetLabel} onSelect={() => (onReset(), close())} />
            </>
          )}
        </>
      )}
    </Menu>
  )
}

// --- the section switcher of a media list («Все / Фильмы / Сериалы / Музыка / Книги») ---

export type MediaSegment = 'all' | 'movies' | 'series' | 'music' | 'books'
export const MEDIA_SEGMENTS: readonly MediaSegment[] = ['all', 'movies', 'series', 'music', 'books']

const MEDIA_SEGMENT_LABEL: Record<Exclude<MediaSegment, 'all'>, Key> = {
  movies: 'common.movies',
  series: 'common.series',
  music: 'common.music',
  books: 'common.books',
}

/** The section of a kind of title or download; audiobooks and comics live in «Книги», as in the library. Unknown kinds: «Все» only. */
export function mediaSegmentOf(kind: string | undefined): Exclude<MediaSegment, 'all'> | null {
  switch (kind) {
    case 'movie':
      return 'movies'
    case 'series':
      return 'series'
    case 'album':
    case 'music':
      return 'music'
    case 'book':
    case 'audiobook':
    case 'comic':
      return 'books'
    default:
      return null
  }
}

const MEDIA_SEGMENT_ICON: Record<Exclude<MediaSegment, 'all'>, IconName> = { movies: 'film', series: 'tv', music: 'music', books: 'book' }

/** A kind's icon (a row without artwork); a film's for an unknown kind. */
export const mediaKindIcon = (kind: string | undefined): IconName => MEDIA_SEGMENT_ICON[mediaSegmentOf(kind) ?? 'movies']

/** A section's icon (its empty state): `all` for «Все». */
export const mediaSegmentIcon = (s: MediaSegment, all: IconName): IconName => (s === 'all' ? all : MEDIA_SEGMENT_ICON[s])

/**
 * The switcher's segments for `kinds` (the list's items): «Все» always, the
 * others only with items, or while chosen (so the switcher does not jump).
 */
export function useMediaSegments(kinds: readonly (string | undefined)[], current: MediaSegment, allLabel: Key): { value: MediaSegment; label: string }[] {
  const t = useT()
  const present = new Set(kinds.map(mediaSegmentOf))
  return MEDIA_SEGMENTS.filter((s) => s === 'all' || present.has(s) || s === current).map((s) => ({
    value: s,
    label: s === 'all' ? t(allLabel) : t(MEDIA_SEGMENT_LABEL[s]),
  }))
}

/** The row under a page's title: the section switcher, then the ViewMenu. */
export function ViewToolbar({ children }: { children: ReactNode }) {
  return <div className={styles.toolbar}>{children}</div>
}

/** A view field: its URL parameter and its values, the first one the default. */
export type ViewSpec<V> = { [K in keyof V]: { param: string; values: readonly V[K][] } }

/** The view kept in the URL's search parameters, and a setter of some of its fields. */
export function useUrlView<V extends Record<string, string>>(spec: ViewSpec<V>): [V, (patch: Partial<V>) => void] {
  const [params, setParams] = useSearchParams()
  const keys = Object.keys(spec) as (keyof V)[]
  const view = {} as V
  for (const k of keys) {
    const { param, values } = spec[k]
    const v = params.get(param)
    view[k] = (values.includes(v as V[typeof k]) ? v : values[0]) as V[typeof k]
  }
  const set = (patch: Partial<V>) => {
    const next = new URLSearchParams(params)
    const v = { ...view, ...patch }
    for (const k of keys) {
      const { param, values } = spec[k]
      if (v[k] === values[0]) next.delete(param)
      else next.set(param, v[k])
    }
    setParams(next, { replace: true })
  }
  return [view, set]
}
