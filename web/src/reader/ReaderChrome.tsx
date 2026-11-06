// The reader's panels: on top «‹ Книги», the title, «Поиск», «Оглавление»
// and «Аа» (size, theme, font, margins, columns); at the bottom the chapter
// and «34 %». They hide 3 s after the last interaction (never while a menu
// or sheet is open, or while focus is in them); a click in the middle of the
// page, any key that does not turn the page, and focus bring them back. Esc
// brings them back first and leaves the book on a second press.
import { useCallback, useEffect, useRef, useState, type FocusEvent, type FormEvent, type ReactNode } from 'react'
import { Icon } from '../components/Icon'
import { MenuHeading, MenuItem, MenuSeparator, Menu } from '../ui/Menu'
import { Sheet } from '../ui/Sheet'
import { TextField } from '../ui/TextField'
import { useIsCompact } from '../state/useMediaQuery'
import {
  clampScale,
  FONTS,
  MARGINS,
  MAX_SCALE,
  MIN_SCALE,
  SCALE_STEP,
  THEMES,
  type ReaderColumns,
  type ReaderFont,
  type ReaderMargin,
  type ReaderPrefs,
  type ReaderTheme,
} from './prefs'
import { AnnotationsSheet, type AnnotationsSheetProps } from './Annotations'
import styles from './ReaderChrome.module.css'
import { formatPercent, useT, type Key } from '../i18n'

export const HIDE_AFTER_MS = 3000

/** Panels shown on start and after `poke`, hidden after 3 s unless `locked`. */
export function useAutoHide(locked: boolean) {
  const [visible, setVisible] = useState(true)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const lockedRef = useRef(locked)
  lockedRef.current = locked

  const arm = useCallback(() => {
    if (timer.current) clearTimeout(timer.current)
    timer.current = setTimeout(() => {
      if (!lockedRef.current) setVisible(false)
    }, HIDE_AFTER_MS)
  }, [])

  const poke = useCallback(() => {
    setVisible(true)
    arm()
  }, [arm])

  const toggle = useCallback(() => {
    setVisible((v) => {
      if (!v) arm()
      return !v
    })
  }, [arm])

  const hide = useCallback(() => {
    if (!lockedRef.current) setVisible(false)
  }, [])

  useEffect(() => {
    if (locked) {
      if (timer.current) clearTimeout(timer.current)
      setVisible(true)
    } else arm()
  }, [locked, arm])

  useEffect(() => () => void (timer.current && clearTimeout(timer.current)), [])

  return { visible, poke, toggle, hide }
}

/** Marks the panels: a key pressed on one of their controls works that control and never turns the page. */
export const CHROME_ATTR = 'data-reader-chrome'

/** The key was pressed on a control of the panels. */
export function inChrome(e: Event): boolean {
  const t = e.target as { closest?: (s: string) => Element | null } | null
  return typeof t?.closest === 'function' && !!t.closest(`[${CHROME_ATTR}]`)
}

/**
 * A key the reader does not use itself (not a page turn): shows the panels.
 * Esc shows them first and, with them up, leaves the book. True when the
 * key was used up here.
 */
export function chromeKey(e: KeyboardEvent, chrome: { visible: boolean; poke: () => void }, onBack: () => void): boolean {
  if (e.key === 'Escape') {
    e.preventDefault()
    if (chrome.visible) onBack()
    else chrome.poke()
    return true
  }
  chrome.poke()
  return false
}

/** Focus inside an element (the panels): for the auto-hide lock. */
export function useFocusInside() {
  const [inside, setInside] = useState(false)
  const onFocus = useCallback(() => setInside(true), [])
  const onBlur = useCallback((e: FocusEvent<HTMLElement>) => {
    if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setInside(false)
  }, [])
  return [inside, { onFocus, onBlur }] as const
}

export interface TocEntry {
  label: string
  href: string
  depth: number
}

export interface SearchHit {
  cfi: string
  /** Chapter of the hit. */
  label: string
  pre: string
  match: string
  post: string
}

export interface ReaderChromeProps {
  title: string
  visible: boolean
  theme: ReaderTheme
  onBack: () => void
  chapter: string
  /** 0..1, or null while unknown. */
  fraction: number | null
  /** Called with true while a menu or sheet is open (keeps the panels up). */
  onLockChange: (locked: boolean) => void
  toc?: TocEntry[]
  currentHref?: string
  onToc?: (href: string) => void
  prefs?: ReaderPrefs
  onPrefs?: (p: ReaderPrefs) => void
  search?: (query: string) => Promise<SearchHit[]>
  onSearchPick?: (hit: SearchHit) => void
  /** The search sheet closed (after a pick or not): stop searching. */
  onSearchClose?: (picked: boolean) => void
  /** A problem to show in the bottom bar (e.g. the position is not saved); keeps the panels up. */
  note?: string | null
  /** More controls before «Аа» (PDF zoom). */
  extra?: ReactNode
  /** The page on screen holds a bookmark (the ribbon is filled). */
  bookmarked?: boolean
  /** «Добавить закладку» / «Убрать закладку» in the top bar. */
  onBookmark?: () => void
  /** «Закладки и заметки»: the list (the sheet's own close is the chrome's). */
  annotations?: Omit<AnnotationsSheetProps, 'onClose'>
  /**
   * «Семья» in the bottom bar: a thin line of the book read so far
   * (`fraction`) with the faces of the other profiles on it (FamilyMarks).
   */
  family?: ReactNode
}

export function ReaderChrome(p: ReaderChromeProps) {
  const t = useT()
  const compact = useIsCompact()
  const [sheet, setSheet] = useState<'toc' | 'search' | 'marks' | null>(null)
  const [menu, setMenu] = useState(false)
  const [focused, focusProps] = useFocusInside()
  const { onLockChange } = p
  useEffect(() => onLockChange(sheet !== null || menu || !!p.note || focused), [sheet, menu, p.note, focused, onLockChange])

  const hidden = !p.visible && sheet === null && !menu && !focused
  // Hidden panels stay reachable with Tab: focus in them shows them (and keeps them up).
  return (
    <div className={styles.chrome} data-theme={p.theme} data-hidden={hidden || undefined} {...{ [CHROME_ATTR]: '' }} {...focusProps}>
      <header className={`${styles.top} ${styles.bar} glass-bar`}>
        <button type="button" className={styles.back} onClick={p.onBack}>
          <Icon name="chevronLeft" size={22} strokeWidth={2.4} />
          <span>{t('common.books')}</span>
        </button>
        <h1 className={styles.title}>{p.title}</h1>
        <div className={styles.tools}>
          {p.extra}
          {p.search && (
            <button type="button" className={styles.tool} aria-label={t('common.search')} onClick={() => setSheet('search')}>
              <Icon name="search" size={20} />
            </button>
          )}
          {p.annotations && (
            <button type="button" className={styles.tool} aria-label={t('reader.annotations')} onClick={() => setSheet('marks')}>
              <Icon name="quote" size={20} />
            </button>
          )}
          {p.onBookmark && (
            <button
              type="button"
              className={styles.tool}
              aria-label={p.bookmarked ? t('reader.bookmark.remove') : t('reader.bookmark.add')}
              aria-pressed={!!p.bookmarked}
              onClick={p.onBookmark}
            >
              <Icon name={p.bookmarked ? 'bookmarkFill' : 'bookmark'} size={20} className={p.bookmarked ? styles.marked : undefined} />
            </button>
          )}
          {p.toc && (
            <button type="button" className={styles.tool} aria-label={t('reader.toc')} onClick={() => setSheet('toc')} disabled={p.toc.length === 0}>
              <Icon name="list" size={21} />
            </button>
          )}
          {p.prefs && p.onPrefs && (
            <Menu
              label={t('reader.appearance')}
              placement="bottom-end"
              onOpenChange={setMenu}
              className={styles.settings}
              trigger={(tp) => (
                <button type="button" {...tp} className={styles.tool} aria-label={t('reader.appearance')}>
                  <span className={styles.aa} aria-hidden>
                    {t('reader.aa')}
                  </span>
                </button>
              )}
            >
              {() => <ReaderSettings prefs={p.prefs!} onChange={p.onPrefs!} compact={compact} />}
            </Menu>
          )}
        </div>
      </header>

      <footer className={`${styles.bottom} ${styles.bar} glass-bar`} aria-hidden={hidden || undefined}>
        {p.note ? (
          <span className={`${styles.chapter} ${styles.note}`} role="status">
            {p.note}
          </span>
        ) : (
          <span className={styles.chapter}>{p.chapter}</span>
        )}
        {p.family && (
          <span className={styles.line}>
            <span className={styles.lineTrack} aria-hidden>
              <span className={styles.lineFill} style={{ width: `${Math.min(1, Math.max(0, p.fraction ?? 0)) * 100}%` }} />
            </span>
            {p.family}
          </span>
        )}
        <span className={styles.percent} aria-label={t('reader.readFraction')}>
          {p.fraction == null ? '' : formatPercent(Math.min(1, Math.max(0, p.fraction)) * 100)}
        </span>
      </footer>

      {sheet === 'toc' && p.toc && (
        <TocSheet
          toc={p.toc}
          currentHref={p.currentHref}
          onPick={(href) => {
            setSheet(null)
            p.onToc?.(href)
          }}
          onClose={() => setSheet(null)}
        />
      )}
      {sheet === 'marks' && p.annotations && (
        <AnnotationsSheet
          {...p.annotations}
          onJump={(a) => {
            setSheet(null)
            p.annotations?.onJump(a)
          }}
          onEditNote={
            p.annotations.onEditNote &&
            ((a) => {
              setSheet(null)
              p.annotations?.onEditNote?.(a)
            })
          }
          onClose={() => setSheet(null)}
        />
      )}
      {sheet === 'search' && p.search && (
        <SearchSheet
          search={p.search}
          onPick={(hit) => {
            setSheet(null)
            p.onSearchClose?.(true)
            p.onSearchPick?.(hit)
          }}
          onClose={() => {
            setSheet(null)
            p.onSearchClose?.(false)
          }}
        />
      )}
    </div>
  )
}

/** Base of a TOC href (without the fragment), to mark the current chapter. */
const baseHref = (h: string | undefined) => (h ?? '').split('#')[0]

function TocSheet({ toc, currentHref, onPick, onClose }: { toc: TocEntry[]; currentHref?: string; onPick: (href: string) => void; onClose: () => void }) {
  const t = useT()
  const exact = toc.findIndex((e) => e.href === currentHref)
  const current = exact >= 0 ? exact : toc.findIndex((e) => baseHref(e.href) === baseHref(currentHref))
  const listRef = useRef<HTMLOListElement>(null)
  useEffect(() => {
    const el = listRef.current?.querySelector<HTMLElement>('[aria-current="true"]')
    el?.scrollIntoView?.({ block: 'center' })
    el?.focus()
  }, [])
  return (
    <Sheet title={t('reader.toc')} role="dialog" size="large" dismissOnOutside onCancel={onClose}>
      <ol ref={listRef} className={styles.toc}>
        {toc.map((e, i) => (
          <li key={`${e.href}-${i}`}>
            <button
              type="button"
              className={styles.tocItem}
              style={{ paddingInlineStart: `${12 + e.depth * 18}px` }}
              aria-current={i === current ? 'true' : undefined}
              onClick={() => onPick(e.href)}
            >
              <span className={styles.tocLabel}>{e.label}</span>
              {i === current && <Icon name="check" size={18} strokeWidth={2.4} />}
            </button>
          </li>
        ))}
      </ol>
    </Sheet>
  )
}

type SearchState = { status: 'idle' } | { status: 'busy' } | { status: 'done'; hits: SearchHit[]; query: string } | { status: 'error' }

function SearchSheet({ search, onPick, onClose }: { search: (q: string) => Promise<SearchHit[]>; onPick: (h: SearchHit) => void; onClose: () => void }) {
  const t = useT()
  const [q, setQ] = useState('')
  const [state, setState] = useState<SearchState>({ status: 'idle' })
  const run = (e?: FormEvent) => {
    e?.preventDefault()
    const query = q.trim()
    if (!query || state.status === 'busy') return
    setState({ status: 'busy' })
    search(query)
      .then((hits) => setState({ status: 'done', hits, query }))
      .catch(() => setState({ status: 'error' }))
  }
  return (
    <Sheet
      title={t('reader.search.title')}
      role="dialog"
      size="large"
      busy={state.status === 'busy'}
      actions={[
        { label: t('reader.search.find'), kind: 'primary', disabled: !q.trim(), run: () => run() },
      ]}
      onCancel={onClose}
    >
      <form onSubmit={run} className={styles.searchForm}>
        <TextField label={t('reader.search.placeholder')} hideLabel placeholder={t('reader.search.placeholder')} type="search" value={q} onChange={(e) => setQ(e.target.value)} />
      </form>
      {state.status === 'busy' && <p className={styles.searchNote}>{t('reader.search.busy')}</p>}
      {state.status === 'error' && <p className={styles.searchNote}>{t('reader.search.failed')}</p>}
      {state.status === 'done' && state.hits.length === 0 && <p className={styles.searchNote}>{t('reader.search.nothing', { query: state.query })}</p>}
      {state.status === 'done' && state.hits.length > 0 && (
        <>
          <p className={styles.searchNote}>{t('reader.search.found', { count: state.hits.length })}</p>
          <ul className={styles.hits} aria-label={t('reader.search.results')}>
            {state.hits.map((h) => (
              <li key={h.cfi}>
                <button type="button" className={styles.hit} onClick={() => onPick(h)}>
                  {h.label && <span className={styles.hitChapter}>{h.label}</span>}
                  <span className={styles.hitText}>
                    {h.pre}
                    <mark>{h.match}</mark>
                    {h.post}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
    </Sheet>
  )
}

const SWATCH_ORDER: ReaderTheme[] = ['original', 'paper', 'night']
const FONT_ORDER: ReaderFont[] = ['system', 'georgia', 'iowan']
const MARGIN_ORDER: ReaderMargin[] = ['narrow', 'normal', 'wide']
const COLUMNS: { value: ReaderColumns; label: Key }[] = [
  { value: 'auto', label: 'common.auto' },
  { value: 'one', label: 'reader.columns.one' },
]

/** The «Аа» menu. Every control is a menu item, so arrows move between them. */
export function ReaderSettings({ prefs, onChange, compact }: { prefs: ReaderPrefs; onChange: (p: ReaderPrefs) => void; compact: boolean }) {
  const t = useT()
  const set = (patch: Partial<ReaderPrefs>) => onChange({ ...prefs, ...patch })
  const scale = (d: number) => set({ fontScale: clampScale(prefs.fontScale + d) })
  return (
    <div className={styles.panel}>
      <div className={styles.sizeRow} role="group" aria-label={t('reader.textSize')}>
        <button
          type="button"
          role="menuitem"
          tabIndex={-1}
          className={styles.sizeButton}
          aria-label={t('reader.smallerText')}
          aria-disabled={prefs.fontScale <= MIN_SCALE || undefined}
          onClick={() => prefs.fontScale > MIN_SCALE && scale(-SCALE_STEP)}
        >
          <span className={styles.smallA}>A−</span>
        </button>
        <span className={styles.sizeValue} aria-live="polite">
          {formatPercent(Math.round(prefs.fontScale * 100))}
        </span>
        <button
          type="button"
          role="menuitem"
          tabIndex={-1}
          className={styles.sizeButton}
          aria-label={t('reader.largerText')}
          aria-disabled={prefs.fontScale >= MAX_SCALE || undefined}
          onClick={() => prefs.fontScale < MAX_SCALE && scale(SCALE_STEP)}
        >
          <span className={styles.bigA}>A+</span>
        </button>
      </div>

      <MenuHeading>{t('reader.theme')}</MenuHeading>
      <div className={styles.swatches} role="group" aria-label={t('reader.theme')}>
        {SWATCH_ORDER.map((th) => (
          <button
            key={th}
            type="button"
            role="menuitemradio"
            tabIndex={-1}
            aria-checked={prefs.theme === th}
            className={styles.swatch}
            onClick={() => set({ theme: th })}
          >
            <span className={styles.swatchDot} style={{ background: THEMES[th].bg, color: THEMES[th].fg }} aria-hidden>
              {t('reader.aa')}
            </span>
            <span className={styles.swatchLabel}>{t(THEMES[th].label)}</span>
          </button>
        ))}
      </div>

      <MenuSeparator />
      <MenuHeading>{t('reader.font')}</MenuHeading>
      {FONT_ORDER.map((f) => (
        <MenuItem key={f} label={t(FONTS[f].label)} checked={prefs.font === f} onSelect={() => set({ font: f })} />
      ))}

      <MenuSeparator />
      <MenuHeading>{t('reader.margins')}</MenuHeading>
      <div className={styles.segments} role="group" aria-label={t('reader.margins')}>
        {MARGIN_ORDER.map((m) => (
          <button
            key={m}
            type="button"
            role="menuitemradio"
            tabIndex={-1}
            aria-checked={prefs.margin === m}
            className={styles.segment}
            onClick={() => set({ margin: m })}
          >
            {t(MARGINS[m].label)}
          </button>
        ))}
      </div>

      {!compact && (
        <>
          <MenuHeading>{t('reader.columns')}</MenuHeading>
          <div className={styles.segments} role="group" aria-label={t('reader.columns')}>
            {COLUMNS.map((c) => (
              <button
                key={c.value}
                type="button"
                role="menuitemradio"
                tabIndex={-1}
                aria-checked={prefs.columns === c.value}
                className={styles.segment}
                onClick={() => set({ columns: c.value })}
              >
                {t(c.label)}
              </button>
            ))}
          </div>
        </>
      )}
    </div>
  )
}

/** Plain icon button for the top bar (PDF zoom). */
export function ChromeButton({ label, onClick, children, disabled }: { label: string; onClick: () => void; children: ReactNode; disabled?: boolean }) {
  return (
    <button type="button" className={styles.tool} aria-label={label} onClick={onClick} disabled={disabled}>
      {children}
    </button>
  )
}
