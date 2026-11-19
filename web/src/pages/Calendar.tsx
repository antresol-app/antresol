// «Календарь»: what comes out of the titles this profile marked.
//   Неделя — a row of episode pictures (a wide Shelf) per day that has
//            anything: the episode's still, the poster when it has none; the
//            title and «С2 Э3» under each; «доступно» when it has a source
//            already (on the server, or one an addon found).
//   Месяц  — a month as a calendar: seven columns Monday to Sunday, a week a
//            row, today ringed, the days of the months around dimmed; a day
//            shows the pictures of what comes out (the still, else the
//            poster; with the names where the cell is wide enough). A tap picks the day: its covers as a row
//            under the month.
import { useEffect, useState } from 'react'
import { errorText, getTrackingCalendar } from '../api/client'
import type { CalendarDay, TrackingItem } from '../api/types'
import { Shelf } from '../components/Shelf'
import { Artwork } from '../ui/Artwork'
import { Button } from '../ui/Button'
import { IconButton } from '../ui/IconButton'
import { SegmentedControl } from '../ui/SegmentedControl'
import { locale, useT, type TFunction } from '../i18n'
import { dayCards, parseYmd, ymd } from './calendarCards'
import styles from './Calendar.module.css'

type Span = 'week' | 'month'

const addDays = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n)
/** Monday of the week of d. */
const mondayOf = (d: Date) => addDays(d, -((d.getDay() + 6) % 7))

/**
 * The days the page asks for: the week (Monday to Sunday) around `anchor`,
 * or the month's grid, its first week's Monday to its last week's Sunday
 * (at most six weeks: within the server's 42 days).
 */
export function rangeOf(span: Span, anchor: Date): { from: Date; to: Date } {
  if (span === 'month') {
    const first = new Date(anchor.getFullYear(), anchor.getMonth(), 1)
    const last = new Date(anchor.getFullYear(), anchor.getMonth() + 1, 0)
    return { from: mondayOf(first), to: addDays(mondayOf(last), 6) }
  }
  const from = mondayOf(anchor)
  return { from, to: addDays(from, 6) }
}

function shift(span: Span, anchor: Date, dir: 1 | -1): Date {
  return span === 'month' ? new Date(anchor.getFullYear(), anchor.getMonth() + dir, 1) : addDays(anchor, 7 * dir)
}

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; days: CalendarDay[] }

export function Calendar() {
  const t = useT()
  const [span, setSpan] = useState<Span>('week')
  const [anchor, setAnchor] = useState(() => new Date())
  // the month's picked day (null: today in its month, else its first day)
  const [picked, setPicked] = useState<string | null>(null)
  const [state, setState] = useState<State>({ status: 'loading' })
  const { from, to } = rangeOf(span, anchor)
  const fromS = ymd(from)
  const toS = ymd(to)
  const today = ymd(new Date())

  useEffect(() => {
    document.title = t('tracking.calendar.title')
  }, [t])

  useEffect(() => {
    const ac = new AbortController()
    setState({ status: 'loading' })
    getTrackingCalendar(fromS, toS, ac.signal)
      .then((days) => setState({ status: 'ready', days }))
      .catch((e: unknown) => {
        if (!ac.signal.aborted)
          setState({
            status: 'error',
            message: errorText(e, t('tracking.calendar.failed')),
          })
      })
    return () => ac.abort()
  }, [fromS, toS, t])

  const move = (next: Date) => {
    setAnchor(next)
    setPicked(null)
  }

  const short = new Intl.DateTimeFormat(locale(), {
    day: 'numeric',
    month: 'short',
  })
  const rangeLabel =
    span === 'month'
      ? new Intl.DateTimeFormat(locale(), {
          month: 'long',
          year: 'numeric',
        }).format(anchor)
      : `${short.format(from)} – ${short.format(to)}`
  const days = state.status === 'ready' ? state.days : []
  const byDate = new Map(days.map((d) => [d.date, d.items]))
  const thisMonth = (s: string) => parseYmd(s).getMonth() === anchor.getMonth()
  const selected = picked ?? (thisMonth(today) && today >= fromS && today <= toS ? today : ymd(new Date(anchor.getFullYear(), anchor.getMonth(), 1)))
  // «Сегодня» is a step away when the shown range does not hold today
  const showsToday = span === 'month' ? thisMonth(today) && parseYmd(today).getFullYear() === anchor.getFullYear() : today >= fromS && today <= toS

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('tracking.calendar.title')}</h1>
        <div className={styles.bar}>
          <SegmentedControl<Span>
            label={t('tracking.calendar.range')}
            size="small"
            value={span}
            onChange={(s) => {
              setSpan(s)
              setPicked(null)
            }}
            segments={[
              { value: 'week', label: t('tracking.calendar.week') },
              { value: 'month', label: t('tracking.calendar.month') },
            ]}
          />
          <span className={styles.range} aria-live="polite">
            {rangeLabel}
          </span>
          <div className={styles.nav}>
            <Button variant="secondary" size="small" disabled={showsToday} onClick={() => move(new Date())}>
              {t('tracking.calendar.today')}
            </Button>
            <IconButton icon="chevronLeft" label={t('tracking.calendar.prev')} onClick={() => move(shift(span, anchor, -1))} />
            <IconButton icon="chevronRight" label={t('tracking.calendar.next')} onClick={() => move(shift(span, anchor, 1))} />
          </div>
        </div>
      </header>

      {state.status === 'error' && (
        <p className={styles.problem} role="alert">
          {state.message}
        </p>
      )}

      {span === 'week' && state.status === 'ready' && days.length === 0 && <p className={styles.empty}>{t('tracking.calendar.empty')}</p>}
      {span === 'week' && days.length > 0 && (
        <div className={styles.rails}>
          {days.map((d) => (
            <Shelf key={d.date} title={dayTitle(t, d.date, today)} cards={dayCards(t, d.items)} variant="wide" contained />
          ))}
        </div>
      )}

      {span === 'month' && state.status !== 'error' && (
        <>
          <MonthGrid from={from} to={to} month={anchor.getMonth()} today={today} selected={selected} byDate={byDate} onPick={setPicked} busy={state.status === 'loading'} />
          {state.status === 'ready' && days.length === 0 ? (
            <p className={styles.empty}>{t('tracking.calendar.empty')}</p>
          ) : (
            state.status === 'ready' && (
              <div className={styles.rails} id="cal-day" aria-live="polite">
                {byDate.get(selected)?.length ? (
                  <Shelf title={dayTitle(t, selected, today)} cards={dayCards(t, byDate.get(selected)!)} variant="wide" contained />
                ) : (
                  <p className={styles.dayEmpty}>
                    <span className={styles.dayEmptyTitle}>{dayTitle(t, selected, today)}</span>
                    {t('tracking.calendar.dayEmpty')}
                  </p>
                )}
              </div>
            )
          )}
        </>
      )}
    </div>
  )
}

/** «Сегодня, четверг, 19 ноября» or «пятница, 20 ноября». */
function dayTitle(t: TFunction, date: string, today: string): string {
  const s = new Intl.DateTimeFormat(locale(), {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
  }).format(parseYmd(date))
  const label = s.charAt(0).toUpperCase() + s.slice(1)
  return date === today ? `${t('tracking.calendar.today')}, ${s}` : label
}

// how many of a day's titles a cell names (a wide cell) or shows as covers (a phone's)
const WIDE_MAX = 3
const NARROW_MAX = 2

function MonthGrid({
  from,
  to,
  month,
  today,
  selected,
  byDate,
  onPick,
  busy,
}: {
  from: Date
  to: Date
  month: number
  today: string
  selected: string
  byDate: Map<string, TrackingItem[]>
  onPick: (d: string) => void
  busy: boolean
}) {
  const t = useT()
  const weekday = new Intl.DateTimeFormat(locale(), { weekday: 'short' })
  const full = new Intl.DateTimeFormat(locale(), {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
  })
  const cells: Date[] = []
  for (let d = from; d <= to; d = addDays(d, 1)) cells.push(d)

  return (
    <div className={styles.month} aria-busy={busy || undefined}>
      <div className={styles.weekdays} aria-hidden>
        {cells.slice(0, 7).map((d) => (
          <span key={d.getDay()}>{weekday.format(d)}</span>
        ))}
      </div>
      <div className={styles.grid}>
        {cells.map((d) => {
          const key = ymd(d)
          const items = byDate.get(key) ?? []
          // one per title, as the day's row («С1 Э1–3»)
          const titles = dayCards(t, items)
          const other = d.getMonth() !== month
          const cls = [styles.cell, other ? styles.other : '', key === today ? styles.today : '', key === selected ? styles.selected : '', titles.length ? styles.busyDay : '']
            .filter(Boolean)
            .join(' ')
          const names = titles.map((c) => c.title).join(', ')
          return (
            <button
              key={key}
              type="button"
              className={cls}
              aria-pressed={key === selected}
              aria-current={key === today ? 'date' : undefined}
              aria-label={names ? `${full.format(d)}: ${names}` : full.format(d)}
              aria-controls="cal-day"
              onClick={() => onPick(key)}
            >
              <span className={styles.num}>{d.getDate()}</span>
              {titles.length > 0 && (
                <span className={styles.items}>
                  {titles.map((c, i) => (
                    <span key={`${c.tmdbKind}-${c.tmdbId}`} className={[styles.item, i >= WIDE_MAX ? styles.wideHidden : '', i >= NARROW_MAX ? styles.narrowHidden : ''].filter(Boolean).join(' ')}>
                      {c.backdrop ? (
                        <Artwork src={c.backdrop} width={48} className={`${styles.thumb} ${styles.still}`} fallback={<CellPoster src={c.poster} />} />
                      ) : (
                        <CellPoster src={c.poster} />
                      )}
                      <span className={styles.itemText}>
                        <span className={styles.itemName}>{c.title}</span>
                        <span className={styles.itemEp}>{c.subtitle}</span>
                      </span>
                    </span>
                  ))}
                  {titles.length > WIDE_MAX && <span className={`${styles.more} ${styles.wideOnly}`}>+{titles.length - WIDE_MAX}</span>}
                  {titles.length > NARROW_MAX && <span className={`${styles.more} ${styles.narrowOnly}`}>+{titles.length - NARROW_MAX}</span>}
                </span>
              )}
            </button>
          )
        })}
      </div>
    </div>
  )
}

/** A month cell's poster thumb (an item without a still, or one that failed). */
function CellPoster({ src }: { src: string }) {
  return <Artwork src={src} width={32} className={styles.thumb} fallback={<span className={styles.thumb} />} />
}

export default Calendar
