// «Семья»: where the other profiles are in this title, under the action bar.
// Folded it is one quiet line: their avatars, each in a progress ring (a
// check when finished), and who is where. Unfolded it lists everyone with
// «С2 Э5 · 34 мин», «стр. 120 из 300» or «Глава 7 · 42 %», when, and a
// button that continues from their place — written to this profile's own
// progress (the server copies it: POST /api/progress/{id}/adopt), never
// theirs. Nothing is shown when nobody else has progress (or for a kid:
// the server lists nobody).
import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { adoptProgress, getFamily } from '../api/client'
import type { BookChapter, FamilyMember } from '../api/types'
import { chapterIndexAt } from '../player/audio/audiobook'
import { clock, humanMinutes } from '../player/timeline'
import { ago } from '../state/format'
import { t as tr, useT } from '../i18n'
import { Avatar } from './Avatar'
import { Button } from '../ui/Button'
import { Icon } from './Icon'
import styles from './FamilyProgress.module.css'

/** What the page is: the labels and the jump depend on it. */
export type FamilyKind = 'movie' | 'episode' | 'series' | 'book' | 'audiobook' | 'comic'

/** The other profiles' places in item `id` (none while loading, on an error or when `enabled` is false). */
export function useFamily(id: number, enabled = true): FamilyMember[] {
  const [members, setMembers] = useState<FamilyMember[]>([])
  useEffect(() => {
    setMembers([])
    if (!enabled || !(id > 0)) return
    const ac = new AbortController()
    getFamily(id, ac.signal)
      .then((m) => !ac.signal.aborted && setMembers(Array.isArray(m) ? m : []))
      .catch(() => {}) // the page works without it
    return () => ac.abort()
  }, [id, enabled])
  return members
}

const pct = (f: number) => Math.round(Math.min(1, Math.max(0, f)) * 100)
const minutes = (ms: number) => humanMinutes(ms / 60_000)

/** «С2 Э5», «Спецвыпуск 1». */
function code(m: FamilyMember): string {
  return m.season === 0 ? tr('comp.episode.special', { n: m.episode ?? 0 }) : tr('comp.episode.code', { season: m.season ?? 0, episode: m.episode ?? 0 })
}

/** «Том 3» (or the volume's own title). */
function volume(m: FamilyMember): string {
  return m.volume ? tr('comp.family.volume', { n: m.volume }) : (m.title ?? '')
}

/** The ring: a series' or comic's share done, else the item's. */
export function ringOf(m: FamilyMember): number {
  if (m.finished) return 1
  if (m.total && m.total > 0) return Math.min(1, (m.done ?? 0) / m.total)
  return Math.min(1, Math.max(0, m.fraction))
}

/** Where m is: «С2 Э5 · 34 мин», «стр. 120 из 300», «Глава 7 · 42 %», «Просмотрено». */
export function placeOf(kind: FamilyKind, m: FamilyMember, chapters: BookChapter[] = []): string {
  if (m.finished) {
    if (kind === 'series') return tr('comp.family.allWatched')
    if (kind === 'book' || kind === 'comic') return tr('comp.family.read')
    if (kind === 'audiobook') return tr('comp.family.heard')
    return tr('comp.family.watched')
  }
  const page = m.page ? (m.pages ? tr('comp.family.pageOf', { n: m.page, of: m.pages }) : tr('comp.family.page', { n: m.page })) : ''
  switch (kind) {
    case 'series':
      if (m.next) return tr('comp.family.next', { code: code(m) })
      return [code(m), minutes(m.positionMs)].filter(Boolean).join(' · ')
    case 'movie':
    case 'episode':
      return m.durationMs > 0 ? tr('comp.family.timeOf', { time: minutes(m.positionMs) || clock(m.positionMs / 1000), of: minutes(m.durationMs) }) : minutes(m.positionMs)
    case 'audiobook': {
      const p = tr('comp.family.percent', { n: pct(m.fraction) })
      return chapters.length > 0 ? `${tr('comp.family.chapter', { n: chapterIndexAt(chapters, m.positionMs) + 1 })} · ${p}` : p
    }
    case 'comic':
      if (m.next) return tr('comp.family.next', { code: volume(m) })
      return [volume(m), page].filter(Boolean).join(' · ')
    case 'book':
      return page || tr('comp.family.percent', { n: pct(m.fraction) })
  }
}

/** The jump button's label, or '' when there is nothing to continue (finished). */
export function jumpLabel(kind: FamilyKind, m: FamilyMember): string {
  if (m.finished) return ''
  const at = clock(m.positionMs / 1000)
  switch (kind) {
    case 'series':
      return m.next || m.positionMs <= 0 ? tr('comp.family.watchEpisode', { code: code(m) }) : tr('comp.family.watchEpisodeAt', { code: code(m), time: at })
    case 'movie':
    case 'episode':
      return m.positionMs > 0 ? tr('comp.family.watchAt', { time: at }) : ''
    case 'audiobook':
      return m.positionMs > 0 ? tr('comp.family.listenAt', { time: at }) : ''
    case 'comic':
      if (m.next) return tr('comp.family.readVolume', { volume: volume(m) })
      return m.page ? tr('comp.family.readPage', { n: m.page }) : tr('comp.family.readVolume', { volume: volume(m) })
    case 'book':
      return m.page ? tr('comp.family.readPage', { n: m.page }) : m.locator ? tr('comp.family.readAt', { n: pct(m.fraction) }) : ''
  }
}

/** Whether jumping to m first takes m's position as this profile's own (a next episode or volume just starts). */
export const adopts = (m: FamilyMember) => !m.finished && !m.next && (m.positionMs > 0 || !!m.locator)

/**
 * Continues from m's place: the server copies m's position into this
 * profile's progress, then `open` starts the player or reader there.
 */
export async function jumpTo(m: FamilyMember, open: (m: FamilyMember) => void | Promise<void>): Promise<void> {
  if (adopts(m)) await adoptProgress(m.itemId, m.userId)
  await open(m)
}

/** An avatar inside its progress ring; a check badge when finished. */
export function RingAvatar({ m, size = 36 }: { m: Pick<FamilyMember, 'name' | 'avatar' | 'finished'> & { ring: number }; size?: number }) {
  const stroke = size >= 36 ? 2.5 : 2
  const box = size + 2 * (stroke + 2)
  const r = (box - stroke) / 2
  const len = 2 * Math.PI * r
  return (
    <span className={styles.ring} style={{ width: box, height: box }}>
      <svg width={box} height={box} viewBox={`0 0 ${box} ${box}`} aria-hidden>
        <circle className={styles.track} cx={box / 2} cy={box / 2} r={r} strokeWidth={stroke} />
        {m.ring > 0 && (
          <circle
            className={m.finished ? styles.full : styles.arc}
            cx={box / 2}
            cy={box / 2}
            r={r}
            strokeWidth={stroke}
            strokeDasharray={`${len * m.ring} ${len}`}
            transform={`rotate(-90 ${box / 2} ${box / 2})`}
          />
        )}
      </svg>
      <Avatar name={m.name} avatar={m.avatar} size={size} className={styles.face} />
      {m.finished && (
        <span className={styles.check} aria-hidden>
          <Icon name="check" size={Math.round(size * 0.3)} strokeWidth={3} />
        </span>
      )}
    </span>
  )
}

export interface FamilyProgressProps {
  members: FamilyMember[]
  kind: FamilyKind
  /** An audiobook's chapters («Глава 7»). */
  chapters?: BookChapter[]
  /** Opens the player or reader at m's place (after it became this profile's position). */
  onOpen: (m: FamilyMember) => void | Promise<void>
}

/** How many faces the folded line shows before «+N». */
const FACES = 4

export function FamilyProgress({ members, kind, chapters, onOpen }: FamilyProgressProps) {
  const t = useT()
  const id = useId()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState<number | null>(null)
  const [error, setError] = useState('')
  const root = useRef<HTMLElement>(null)
  const fold = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const [up, setUp] = useState(false)

  // over the page (wide screens): upwards when there is no room below
  useLayoutEffect(() => {
    if (!open || !panel.current || !root.current) return setUp(false)
    if (getComputedStyle(panel.current).position !== 'absolute') return setUp(false)
    const below = window.innerHeight - root.current.getBoundingClientRect().bottom
    setUp(below < panel.current.offsetHeight + 16 && root.current.getBoundingClientRect().top > panel.current.offsetHeight + 16)
  }, [open, members.length])

  // like a menu: a click elsewhere or Escape folds it
  useEffect(() => {
    if (!open) return
    const onDown = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      // the panel goes away: focus inside it (or already lost) returns to the fold
      const at = document.activeElement
      if (!at || at === document.body || root.current?.contains(at)) fold.current?.focus()
      setOpen(false)
    }
    document.addEventListener('pointerdown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('pointerdown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  if (members.length === 0) return null

  const first = members[0]!
  const more = members.length - 1
  const summary = `${first.name} · ${placeOf(kind, first, chapters)}`

  const jump = (m: FamilyMember) => {
    setBusy(m.userId)
    setError('')
    jumpTo(m, onOpen)
      .catch(() => setError(t('comp.action.failed')))
      .finally(() => setBusy(null))
  }

  return (
    <section ref={root} className={styles.family} aria-label={t('comp.family.title')}>
      <button ref={fold} type="button" className={styles.fold} aria-expanded={open} aria-controls={`${id}-list`} onClick={() => setOpen((v) => !v)}>
        <span className={styles.faces} aria-hidden>
          {members.slice(0, FACES).map((m) => (
            <RingAvatar key={m.userId} m={{ ...m, ring: ringOf(m) }} size={28} />
          ))}
          {members.length > FACES && <span className={styles.extra}>+{members.length - FACES}</span>}
        </span>
        <span className={styles.foldText}>
          <span className={styles.label}>{t('comp.family.title')}</span>
          <span className={styles.summary}>
            {summary}
            {more > 0 && <span className={styles.more}>{t('comp.family.andMore', { count: more })}</span>}
          </span>
        </span>
        <Icon name="chevronDown" size={16} strokeWidth={2.4} className={[styles.chevron, open ? styles.up : ''].join(' ')} />
      </button>
      {open && (
        <div ref={panel} id={`${id}-list`} className={[styles.panel, up ? styles.upward : '', 'glass-strong'].join(' ')} data-family-panel>
          <ul className={styles.list}>
            {members.map((m) => {
              const label = jumpLabel(kind, m)
              return (
                <li key={m.userId} className={styles.row}>
                  <RingAvatar m={{ ...m, ring: ringOf(m) }} size={36} />
                  <span className={styles.who}>
                    <span className={styles.name}>{m.name}</span>
                    <span className={styles.place}>
                      {placeOf(kind, m, chapters)}
                      <span className={styles.when}> · {ago(m.updatedAt)}</span>
                    </span>
                  </span>
                  {label && (
                    <Button
                      size="small"
                      variant="secondary"
                      icon={kind === 'book' || kind === 'comic' ? 'book' : 'play'}
                      loading={busy === m.userId}
                      disabled={busy !== null && busy !== m.userId}
                      aria-label={t('comp.family.jumpAria', { action: label, name: m.name })}
                      onClick={() => jump(m)}
                    >
                      {label}
                    </Button>
                  )}
                </li>
              )
            })}
          </ul>
          <p className={styles.note}>{error ? <span role="alert">{error}</span> : t('comp.family.note')}</p>
        </div>
      )}
    </section>
  )
}

/** «, здесь семья: Мама, Папа»: the faces of an episode tile, for its label (the faces themselves are hidden). */
export const tileFacesLabel = (members: FamilyMember[]) =>
  members.length === 0 ? '' : tr('comp.family.tileAria', { names: members.map((m) => m.name).join(', ') })

/** Who of the family is on an episode: small faces for its tile (EpisodeList; its label names them, tileFacesLabel). */
export function TileFaces({ members, className }: { members: FamilyMember[]; className?: string }) {
  if (members.length === 0) return null
  return (
    <span className={[styles.tileFaces, className].filter(Boolean).join(' ')} title={members.map((m) => m.name).join(', ')} aria-hidden>
      {members.slice(0, 3).map((m) => (
        <Avatar key={m.userId} name={m.name} avatar={m.avatar} size={20} className={styles.face} />
      ))}
    </span>
  )
}
