// «Телегид»: the programme guide grid — channels down, time across. The
// channel column (number, name, star) stays at the left and the time
// ruler (half-hour ticks) at the top while the grid scrolls both ways; a
// programme is a block as wide as it lasts; the line marks now; what has
// been is dimmed, what is on is lit. It opens an hour back and reads the
// next six hours as it scrolls towards them. A block opens its details
// (ProgramSheet). Keys: arrows move between blocks (up/down keep the time),
// Enter opens, «Сейчас» returns to now.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from 'react'
import { useNavigate } from 'react-router'
import { errorText } from '../../api/client'
import { getChannelEPG, type ChannelEPG, type ChannelProgram } from '../../api/channels'
import { Icon } from '../../components/Icon'
import { useT } from '../../i18n'
import { useIsCompact } from '../../state/useMediaQuery'
import { Button } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { applyFilter, blockOf, gridMove, guideOrigin, labelInset, mergePrograms, MIN, ticksOf, type ChannelFilter } from './model'
import { ChannelLogo } from './ChannelLogo'
import { ProgramSheet } from './ProgramSheet'
import { clock, useNow } from './useChannels'
import styles from './Guide.module.css'

/** One window of the guide (hours). */
const PAGE_H = 6
/** The schedule reaches this far ahead (the server keeps 48 h). */
const AHEAD_MS = 47 * 60 * MIN
/** The hairline between two programmes (px). */
const GAP = 2

export interface GuideProps {
  filter: ChannelFilter
  /** The profile's stars as the list knows them (they turn at once). */
  favorites: Record<number, boolean>
  onFavorite: (id: number, on: boolean) => void
  /** The player's address for a channel (keeps the filter). */
  playerTo: (id: number) => string
  /** The channels' logos as the list knows them (an EPG window may leave them out). */
  logos?: Record<number, string | undefined>
}

export function Guide({ filter, favorites, onFavorite, playerTo, logos }: GuideProps) {
  const t = useT()
  const compact = useIsCompact()
  const navigate = useNavigate()
  const now = useNow(30_000)
  const ppm = compact ? 4 : 6 // px per minute: 30 min = 120 / 180 px
  const [origin] = useState(() => guideOrigin(Date.now()))
  const [data, setData] = useState<{ end: number; channels: ChannelEPG[] } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const loading = useRef(false)
  const [open, setOpen] = useState<{ row: number; col: number } | null>(null)
  const [focus, setFocus] = useState<{ row: number; col: number; anchor: number } | null>(null)
  const scroller = useRef<HTMLDivElement>(null)

  const loadMore = useCallback(
    (from: number, first: boolean) => {
      if (loading.current) return
      loading.current = true
      getChannelEPG(new Date(from), PAGE_H).then(
        (w) => {
          loading.current = false
          setError(null)
          setData((d) => {
            if (first || !d) return { end: Date.parse(w.to), channels: w.channels }
            const add = new Map(w.channels.map((c) => [c.id, c]))
            return {
              end: Math.max(d.end, Date.parse(w.to)),
              channels: d.channels.map((c) => ({ ...c, programs: mergePrograms(c.programs, add.get(c.id)?.programs ?? []) })),
            }
          })
        },
        (e: unknown) => {
          loading.current = false
          setError(errorText(e, t('tv.guideFailed')))
        },
      )
    },
    [t],
  )
  useEffect(() => loadMore(origin, true), [loadMore, origin])

  const end = data?.end ?? origin + PAGE_H * 60 * MIN
  const width = ((end - origin) / MIN) * ppm
  const rows = useMemo(() => {
    const list = (data?.channels ?? []).map((c) => ({ ...c, favorite: favorites[c.id] ?? c.favorite }))
    return applyFilter(list, filter).map((c) => ({
      channel: c,
      programs: c.programs.filter((p) => Date.parse(p.end) > origin && Date.parse(p.start) < end),
    }))
  }, [data, favorites, filter, origin, end])
  // a logo in any row: the rows without one keep its slot (the names line up)
  const anyLogo = rows.some((r) => r.channel.logoUrl ?? logos?.[r.channel.id])

  // the visible time's left edge (track px): the labels of blocks begun before it stay in sight
  const [viewLeft, setViewLeft] = useState(0)
  const frame = useRef(0)
  // the next window, when the end comes into sight
  const onScroll = () => {
    const el = scroller.current
    if (el && !frame.current)
      frame.current = window.requestAnimationFrame(() => {
        frame.current = 0
        setViewLeft(scroller.current?.scrollLeft ?? 0)
      })
    if (!el || !data || error) return
    if (el.scrollLeft + el.clientWidth > el.scrollWidth - 480 && data.end < Date.now() + AHEAD_MS) loadMore(data.end, false)
  }

  // a logo column widens it by the logo and its gap (ChannelLogo xs 24×1.6 / sm 30×1.6 + 10)
  const chanW = (compact ? 144 : 264) + (anyLogo ? (compact ? 48 : 58) : 0)
  const toNow = useCallback(
    (smooth: boolean) => {
      const el = scroller.current
      if (!el) return
      // now a quarter into the visible time
      const visible = el.clientWidth - chanW
      el.scrollTo({ left: Math.max(0, ((Date.now() - origin) / MIN) * ppm - visible / 4), behavior: smooth ? 'smooth' : 'auto' })
    },
    [chanW, origin, ppm],
  )
  const placed = useRef(false)
  useLayoutEffect(() => {
    if (!data || placed.current) return
    placed.current = true
    toNow(false)
  }, [data, toNow])

  // the guide fills the window down to the tab bar: its own scroller
  const [top, setTop] = useState(0)
  useLayoutEffect(() => {
    const measure = () => setTop(scroller.current ? scroller.current.getBoundingClientRect().top + window.scrollY : 0)
    measure()
    window.addEventListener('resize', measure)
    return () => window.removeEventListener('resize', measure)
  }, [data !== null])

  const cell = (r: number, c: number) => scroller.current?.querySelector<HTMLButtonElement>(`[data-cell="${r}:${c}"]`)
  const onKey = (e: KeyboardEvent) => {
    const keys: Record<string, 'left' | 'right' | 'up' | 'down'> = { ArrowLeft: 'left', ArrowRight: 'right', ArrowUp: 'up', ArrowDown: 'down' }
    const k = keys[e.key]
    if (!k || !focus) return
    const ps = rows.map((r) => r.programs)
    const to = gridMove(ps, focus.row, focus.col, k, focus.anchor)
    if (!to) return
    e.preventDefault()
    const p = ps[to[0]]?.[to[1]]
    if (!p) return
    const anchor = k === 'up' || k === 'down' ? focus.anchor : Math.max(Date.parse(p.start), origin)
    setFocus({ row: to[0], col: to[1], anchor })
    const el = cell(to[0], to[1])
    el?.focus({ preventScroll: false })
    el?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }
  // the one cell in the tab order: the focused one, else the first on air
  const tabCell = useMemo(() => {
    if (focus && rows[focus.row]?.programs[focus.col]) return focus
    const c = rows[0]?.programs.findIndex((p) => Date.parse(p.end) > now) ?? -1
    return { row: 0, col: Math.max(0, c), anchor: now }
  }, [focus, rows, now])

  const ticks = ticksOf(origin, end, ppm)
  const nowX = ((now - origin) / MIN) * ppm
  const opened = open ? rows[open.row] : null
  const openedP: ChannelProgram | undefined = opened?.programs[open!.col]

  if (error && !data) {
    return (
      <div className={styles.empty} role="alert">
        <Icon name="warning" size={40} strokeWidth={1.4} />
        <p>{error}</p>
        <Button variant="secondary" onClick={() => loadMore(origin, true)}>
          {t('common.retry')}
        </Button>
      </div>
    )
  }

  return (
    <div className={styles.wrap}>
      <div
        ref={scroller}
        className={styles.guide}
        style={{ '--chan-w': `${chanW}px`, '--track-w': `${width}px`, '--guide-top': `${top}px` } as CSSProperties}
        onScroll={onScroll}
        onKeyDown={onKey}
        role="grid"
        aria-label={t('tv.guideAria')}
        aria-busy={!data || undefined}
        data-testid="guide"
      >
        <div className={styles.inner}>
          <div className={styles.ruler} role="row">
            <div className={styles.corner} role="columnheader">
              <Button variant="secondary" size="small" onClick={() => toNow(true)}>
                {t('tv.guideNow')}
              </Button>
            </div>
            <div className={styles.ticks} aria-hidden>
              {ticks.map((k) => (
                <span key={k.t} className={styles.tick} data-hour={k.hour || undefined} style={{ left: k.x }}>
                  <span className={styles.tickLabel}>{clock(k.t)}</span>
                </span>
              ))}
              {nowX >= viewLeft && nowX <= width && (
                <span className={styles.nowPill} style={{ left: nowX }}>
                  {clock(now)}
                </span>
              )}
            </div>
          </div>

          {!data && <p className={styles.loading}>{t('tv.guideLoading')}</p>}

          {rows.map(({ channel: c, programs }, r) => {
            const fav = Boolean(c.favorite)
            return (
              <div key={c.id} className={styles.row} role="row">
                <div className={styles.chan} role="rowheader">
                  <span className={styles.chanNum}>{c.number}</span>
                  {anyLogo && <ChannelLogo url={c.logoUrl ?? logos?.[c.id]} size={compact ? 'xs' : 'sm'} reserve />}
                  <span className={styles.chanName} title={c.name}>
                    {c.name}
                  </span>
                  <IconButton
                    className={styles.chanStar}
                    icon={fav ? 'starFill' : 'star'}
                    size="xs"
                    variant="plain"
                    label={fav ? t('tv.favRemove') : t('tv.favAdd')}
                    aria-pressed={fav}
                    data-fav={fav || undefined}
                    onClick={() => onFavorite(c.id, !fav)}
                  />
                </div>
                <div className={styles.track}>
                  {programs.map((p, i) => {
                    const b = blockOf(p, origin, end, ppm, now)
                    if (!b) return null
                    const time = t('tv.time', { start: clock(p.start), end: clock(p.end) })
                    const isTab = tabCell.row === r && tabCell.col === i
                    const inset = labelInset(b.left, b.width - GAP, viewLeft)
                    return (
                      <button
                        key={p.id ?? p.start}
                        type="button"
                        role="gridcell"
                        className={styles.block}
                        style={{ left: b.left, width: Math.max(0, b.width - GAP), '--inset': `${inset}px` } as CSSProperties}
                        data-cell={`${r}:${i}`}
                        data-past={b.past || undefined}
                        data-live={b.live || undefined}
                        data-narrow={b.width - GAP - inset < 56 || undefined}
                        tabIndex={isTab ? 0 : -1}
                        aria-label={t('tv.programAria', { title: [p.title, p.subtitle].filter(Boolean).join(', '), time })}
                        title={`${p.title} · ${time}`}
                        onFocus={() => (focus?.row === r && focus.col === i ? undefined : setFocus({ row: r, col: i, anchor: Math.max(Date.parse(p.start), origin) }))}
                        onClick={() => setOpen({ row: r, col: i })}
                      >
                        <span className={styles.blockTitle}>{p.title}</span>
                        <span className={styles.blockTime}>{p.subtitle ? `${clock(p.start)} · ${p.subtitle}` : clock(p.start)}</span>
                      </button>
                    )
                  })}
                </div>
              </div>
            )
          })}

          {/* not while now has scrolled under the channel column */}
          {nowX >= viewLeft && nowX <= width && <div className={styles.nowLine} style={{ left: `calc(var(--chan-w) + ${nowX}px)` }} aria-hidden />}
        </div>
      </div>

      {opened && openedP && (
        <ProgramSheet
          program={openedP}
          channel={opened.channel}
          now={now}
          onWatch={() => navigate(playerTo(opened.channel.id))}
          onFavorite={(on) => onFavorite(opened.channel.id, on)}
          onClose={() => {
            const o = open
            setOpen(null)
            if (o) window.setTimeout(() => cell(o.row, o.col)?.focus({ preventScroll: true }), 0)
          }}
        />
      )}
    </div>
  )
}
