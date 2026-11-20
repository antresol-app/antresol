// «ТВ»'s categories, as TiviMate has them: «Избранное» first, «Все каналы»,
// the topics (the server's most common tags: the AI helper's and the
// playlist groups', one spelling each), the sources, and «Для взрослых»
// always last (exclusive: its channels are in no other list), each with how
// many channels it has. A row is a link (?f=…).
//
// Two forms: `page` — a phone's first level of a big list (the second level
// is the chosen category's channels, and Back returns here); `rail` — the
// wide window's left column beside the channels, the chosen one marked.
// On the rail ↑/↓ move between categories and open each at once (as a TV
// remote does), → goes to the channel list (onEnterList), Home/End to the
// first and last.
import { useRef, type KeyboardEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import type { ChannelCategory } from '../../api/channels'
import { useT, type TFunction } from '../../i18n'
import { GroupedSection, ListLinkRow } from '../../ui/List'
import { ADULT } from './model'
import styles from './Tv.module.css'

export interface ChannelCategoriesProps {
  categories: ChannelCategory[]
  all: number
  favorites: number
  href: (f: string) => string
  /** The rail's chosen filter (filterParam): fav | all | k:<id>. */
  current?: string
  variant?: 'page' | 'rail'
  /** → on the rail: focus the channel list. */
  onEnterList?: () => void
}

type Row = { key: string; label: string; count: number }
type Section = { title?: string; rows: Row[] }

/** The rows in their order, in sections: the lists (and «Из медиатеки»), the topics, the languages, the sources, the exclusive one. */
export function categorySections(categories: ChannelCategory[], all: number, favorites: number, t: TFunction): Section[] {
  const row = (c: ChannelCategory): Row => ({
    key: `k:${c.id}`,
    label: c.name,
    count: c.count,
  })
  const sections: Section[] = [
    {
      rows: [
        { key: 'fav', label: t('tv.filterFav'), count: favorites },
        { key: 'all', label: t('tv.allChannels'), count: all },
        ...categories.filter((c) => c.kind === 'own').map(row),
      ],
    },
    {
      title: t('tv.categoryGroup.ai'),
      rows: categories.filter((c) => (c.kind === 'ai' || c.kind === 'group') && c.id !== ADULT).map(row),
    },
    {
      title: t('tv.categoryGroup.lang'),
      rows: categories.filter((c) => c.kind === 'lang').map(row),
    },
    {
      title: t('tv.categoryGroup.source'),
      rows: categories.filter((c) => c.kind === 'source').map(row),
    },
    { rows: categories.filter((c) => c.id === ADULT).map(row) },
  ]
  return sections.filter((s) => s.rows.length > 0)
}

export function ChannelCategories({ categories, all, favorites, href, current, variant = 'page', onEnterList }: ChannelCategoriesProps) {
  const t = useT()
  const navigate = useNavigate()
  const ref = useRef<HTMLElement>(null)
  const sections = categorySections(categories, all, favorites, t)

  if (variant === 'page') {
    return (
      <nav className={styles.categories} aria-label={t('tv.categories')}>
        {sections.map((sec, i) => (
          <GroupedSection key={i} title={sec.title}>
            {sec.rows.map((r) => (
              <ListLinkRow key={r.key} label={r.label} value={String(r.count)} to={href(r.key)} />
            ))}
          </GroupedSection>
        ))}
      </nav>
    )
  }

  const keys = sections.flatMap((sec) => sec.rows.map((r) => r.key))
  const onKeyDown = (e: KeyboardEvent<HTMLElement>) => {
    const links = [...(ref.current?.querySelectorAll<HTMLAnchorElement>('a[data-key]') ?? [])]
    const i = links.findIndex((a) => a === document.activeElement)
    if (i < 0) return
    let next = -1
    if (e.key === 'ArrowDown') next = Math.min(links.length - 1, i + 1)
    else if (e.key === 'ArrowUp') next = Math.max(0, i - 1)
    else if (e.key === 'Home') next = 0
    else if (e.key === 'End') next = links.length - 1
    else if (e.key === 'ArrowRight' && onEnterList) {
      e.preventDefault()
      onEnterList()
      return
    } else return
    e.preventDefault()
    const a = links[next]!
    a.focus()
    // as a remote: the category opens as the focus lands on it
    navigate(href(keys[next]!), { replace: true })
  }
  return (
    <nav ref={ref} className={styles.rail} aria-label={t('tv.categories')} onKeyDown={onKeyDown}>
      {sections.map((sec, i) => (
        <div key={i} className={styles.railSection}>
          {sec.title && <h2 className={styles.railTitle}>{sec.title}</h2>}
          {sec.rows.map((r) => (
            <Link
              key={r.key}
              to={href(r.key)}
              replace
              data-key={r.key}
              className={styles.railRow}
              aria-current={current === r.key ? 'page' : undefined}
              tabIndex={current === r.key || (!keys.includes(current ?? '') && r.key === 'all') ? 0 : -1}
            >
              <span className={styles.railLabel}>{r.label}</span>
              <span className={styles.railCount}>{r.count}</span>
            </Link>
          ))}
        </div>
      ))}
    </nav>
  )
}
