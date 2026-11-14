// «Календарь»: what comes out of the titles this profile marked, by day,
// for a week or a month. A row says the episode; «доступно» when it has a
// source already (on the server, or found by a tracker).
import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { errorText, getTrackingCalendar } from '../api/client'
import type { CalendarDay, TrackingItem } from '../api/types'
import { itemHref } from '../components/Card'
import { IconButton } from '../ui/IconButton'
import { Badge } from '../ui/Badge'
import { GroupedSection, ListRow } from '../ui/List'
import { SegmentedControl } from '../ui/SegmentedControl'
import { locale, useT } from '../i18n'
import styles from './Calendar.module.css'

type Span = 'week' | 'month'

/** YYYY-MM-DD of a local date (the server's days are the profile's calendar days). */
const ymd = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
const parse = (s: string) => new Date(Number(s.slice(0, 4)), Number(s.slice(5, 7)) - 1, Number(s.slice(8, 10)))

/** The week (Monday to Sunday) or the month around `anchor`. */
export function rangeOf(span: Span, anchor: Date): { from: Date; to: Date } {
  if (span === 'month') return { from: new Date(anchor.getFullYear(), anchor.getMonth(), 1), to: new Date(anchor.getFullYear(), anchor.getMonth() + 1, 0) }
  const from = new Date(anchor.getFullYear(), anchor.getMonth(), anchor.getDate() - ((anchor.getDay() + 6) % 7))
  return { from, to: new Date(from.getFullYear(), from.getMonth(), from.getDate() + 6) }
}

function shift(span: Span, anchor: Date, dir: 1 | -1): Date {
  return span === 'month' ? new Date(anchor.getFullYear(), anchor.getMonth() + dir, 1) : new Date(anchor.getFullYear(), anchor.getMonth(), anchor.getDate() + 7 * dir)
}

/** Where a title's row leads: its library page, or (sources build) its TMDB page; none otherwise. */
function titleHref(it: TrackingItem): string | null {
  if (it.title.itemId) return itemHref(it.title.kind, it.title.itemId)
  return __LIBRARY_EDITION__ ? null : `/title/${it.title.kind}/${it.title.tmdbId}`
}

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; days: CalendarDay[] }

export function Calendar() {
  const t = useT()
  const [span, setSpan] = useState<Span>('week')
  const [anchor, setAnchor] = useState(() => new Date())
  const [state, setState] = useState<State>({ status: 'loading' })
  const { from, to } = rangeOf(span, anchor)
  const fromS = ymd(from)
  const toS = ymd(to)

  useEffect(() => {
    document.title = t('tracking.calendar.title')
  }, [t])

  useEffect(() => {
    const ac = new AbortController()
    setState({ status: 'loading' })
    getTrackingCalendar(fromS, toS, ac.signal)
      .then((days) => setState({ status: 'ready', days }))
      .catch((e: unknown) => {
        if (!ac.signal.aborted) setState({ status: 'error', message: errorText(e, t('tracking.calendar.failed')) })
      })
    return () => ac.abort()
  }, [fromS, toS, t])

  const day = new Intl.DateTimeFormat(locale(), { weekday: 'long', day: 'numeric', month: 'long' })
  const short = new Intl.DateTimeFormat(locale(), { day: 'numeric', month: 'short' })
  const rangeLabel = span === 'month' ? new Intl.DateTimeFormat(locale(), { month: 'long', year: 'numeric' }).format(from) : `${short.format(from)} – ${short.format(to)}`

  const episode = (it: TrackingItem) => {
    if (it.title.kind === 'movie') return t('tracking.calendar.movie')
    const se = t('tracking.calendar.episode', { season: it.season, episode: it.episode })
    return it.name ? `${se} · ${it.name}` : se
  }

  return (
    <div className={styles.page}>
      <div className={styles.column}>
        <h1 className={styles.title}>{t('tracking.calendar.title')}</h1>
        <div className={styles.bar}>
          <SegmentedControl<Span>
            label={t('tracking.calendar.range')}
            size="small"
            value={span}
            onChange={setSpan}
            segments={[
              { value: 'week', label: t('tracking.calendar.week') },
              { value: 'month', label: t('tracking.calendar.month') },
            ]}
          />
          <span className={styles.range} aria-live="polite">
            {rangeLabel}
          </span>
          <div className={styles.nav}>
            <IconButton icon="chevronLeft" label={t('tracking.calendar.prev')} onClick={() => setAnchor((a) => shift(span, a, -1))} />
            <IconButton icon="chevronRight" label={t('tracking.calendar.next')} onClick={() => setAnchor((a) => shift(span, a, 1))} />
          </div>
        </div>

        {state.status === 'error' && (
          <p className={styles.problem} role="alert">
            {state.message}
          </p>
        )}
        {state.status === 'ready' && state.days.length === 0 && <p className={styles.empty}>{t('tracking.calendar.empty')}</p>}
        {state.status === 'ready' && state.days.length > 0 && (
          <div className={styles.days}>
            {state.days.map((d) => (
              <GroupedSection key={d.date} title={day.format(parse(d.date))} id={`cal-${d.date}`}>
                {d.items.map((it) => {
                  const href = titleHref(it)
                  return (
                    <ListRow
                      key={`${it.title.kind}-${it.title.tmdbId}-${it.season}-${it.episode}`}
                      label={href ? <Link to={href} className={styles.link}>{it.title.name}</Link> : it.title.name}
                      hint={episode(it)}
                    >
                      {it.firstSeen && <Badge tone="success">{t('tracking.calendar.available')}</Badge>}
                    </ListRow>
                  )
                })}
              </GroupedSection>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

export default Calendar
