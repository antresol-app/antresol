// The reader's bookmarks, highlights and notes: «Закладки и заметки» (the
// list, with jump-to, editing and the Markdown export), the note sheet, and
// the menu over selected text or a tapped highlight («Выделить» in four
// colours, «Заметка», «Убрать выделение»). The menu is a toolbar: ←/→ move
// between its buttons, Esc closes it.
import { useEffect, useRef, useState, type KeyboardEvent } from 'react'
import type { Annotation, AnnotationColor } from '../api/types'
import { ButtonAnchor } from '../ui/Button'
import { Icon } from '../components/Icon'
import { SegmentedControl } from '../ui/SegmentedControl'
import { Sheet } from '../ui/Sheet'
import { ANNOTATION_COLORS } from '../offline/annotations'
import { HIGHLIGHT_FILL, type ScreenRect } from './highlights'
import { formatPercent, useT, type Key } from '../i18n'
import styles from './Annotations.module.css'

export const COLOR_NAME: Record<AnnotationColor, Key> = {
  yellow: 'reader.highlight.yellow',
  green: 'reader.highlight.green',
  blue: 'reader.highlight.blue',
  pink: 'reader.highlight.pink',
}

type Tab = 'bookmarks' | 'highlights'

export interface AnnotationsSheetProps {
  list: Annotation[]
  onJump: (a: Annotation) => void
  onRemove: (a: Annotation) => void
  /** Highlights and notes too (EPUB/FB2); PDF has bookmarks only. */
  highlights?: boolean
  onEditNote?: (a: Annotation) => void
  /** The Markdown export (shown when there is something to export). */
  exportUrl?: string
  /** Ids the server refused: «Не удалось сохранить заметку» on their rows. */
  failed?: Set<string>
  onClose: () => void
}

const where = (a: Annotation) => [a.label, formatPercent(Math.round(a.fraction * 100))].filter(Boolean).join(' · ')

export function AnnotationsSheet({ list, onJump, onRemove, highlights, onEditNote, exportUrl, failed, onClose }: AnnotationsSheetProps) {
  const t = useT()
  const marks = list.filter((a) => a.kind === 'bookmark')
  const texts = list.filter((a) => a.kind !== 'bookmark')
  const [tab, setTab] = useState<Tab>(highlights && marks.length === 0 && texts.length > 0 ? 'highlights' : 'bookmarks')
  const shown = tab === 'bookmarks' || !highlights ? marks : texts
  return (
    <Sheet title={highlights ? t('reader.annotations') : t('reader.annotations.bookmarks')} role="dialog" size="large" dismissOnOutside onCancel={onClose}>
      {highlights && (
        <div className={styles.tabs}>
          <SegmentedControl
            label={t('reader.annotations')}
            value={tab}
            onChange={setTab}
            segments={[
              { value: 'bookmarks', label: t('reader.annotations.bookmarks') },
              { value: 'highlights', label: t('reader.annotations.highlights') },
            ]}
          />
        </div>
      )}
      {shown.length === 0 ? (
        <p className={styles.empty}>{tab === 'bookmarks' || !highlights ? t('reader.annotations.noBookmarks') : t('reader.annotations.noHighlights')}</p>
      ) : (
        <ul className={styles.list}>
          {shown.map((a) => {
            const name = a.excerpt || a.label || where(a)
            return (
              <li key={a.id} className={styles.row}>
                <button type="button" className={styles.jump} onClick={() => onJump(a)}>
                  {a.kind === 'bookmark' ? (
                    <Icon name="bookmarkFill" size={18} className={styles.ribbon} />
                  ) : (
                    <span className={styles.swatch} style={{ background: HIGHLIGHT_FILL[(a.color || 'yellow') as AnnotationColor] }} aria-hidden />
                  )}
                  <span className={styles.text}>
                    {a.excerpt && <span className={styles.excerpt}>{a.excerpt}</span>}
                    {a.note && <span className={styles.note}>{a.note}</span>}
                    <span className={styles.where}>{where(a)}</span>
                    {failed?.has(a.id) && (
                      <span className={styles.failed} role="status">
                        {t('reader.annotations.notSaved')}
                      </span>
                    )}
                  </span>
                </button>
                {onEditNote && a.kind !== 'bookmark' && (
                  <button type="button" className={styles.icon} aria-label={t('reader.annotations.editNote')} onClick={() => onEditNote(a)}>
                    <Icon name="pencil" size={18} />
                  </button>
                )}
                <button type="button" className={styles.icon} aria-label={t('reader.annotations.deleteOne', { name })} onClick={() => onRemove(a)}>
                  <Icon name="trash" size={18} />
                </button>
              </li>
            )
          })}
        </ul>
      )}
      {exportUrl && texts.length + marks.length > 0 && (
        <div className={styles.export}>
          <ButtonAnchor href={exportUrl} download variant="secondary" icon="download">
            {t('reader.annotations.export')}
          </ButtonAnchor>
        </div>
      )}
    </Sheet>
  )
}

export interface NoteSheetProps {
  /** The highlighted text the note is about. */
  excerpt: string
  note: string
  color: AnnotationColor
  onSave: (note: string, color: AnnotationColor) => void
  onClose: () => void
}

export function NoteSheet({ excerpt, note, color: initialColor, onSave, onClose }: NoteSheetProps) {
  const t = useT()
  const [text, setText] = useState(note)
  const [color, setColor] = useState(initialColor)
  const ref = useRef<HTMLTextAreaElement>(null)
  useEffect(() => ref.current?.focus(), [])
  return (
    <Sheet
      title={t('reader.note')}
      role="dialog"
      size="wide"
      onCancel={onClose}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onClose },
        { label: t('common.save'), kind: 'primary', run: () => onSave(text.trim(), color) },
      ]}
    >
      {excerpt && <blockquote className={styles.quote}>{excerpt}</blockquote>}
      <textarea
        ref={ref}
        className={styles.textarea}
        value={text}
        maxLength={10000}
        aria-label={t('reader.note')}
        placeholder={t('reader.note.placeholder')}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          // Enter is a new line here; ⌘/Ctrl+Enter saves
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            e.preventDefault()
            onSave(text.trim(), color)
          } else if (e.key === 'Enter') e.stopPropagation()
        }}
      />
      <ColorRow value={color} onPick={setColor} />
    </Sheet>
  )
}

function ColorRow({ value, onPick }: { value: AnnotationColor | ''; onPick: (c: AnnotationColor) => void }) {
  const t = useT()
  return (
    <div className={styles.colors} role="radiogroup" aria-label={t('reader.highlight.color')}>
      {ANNOTATION_COLORS.map((c) => (
        <button
          key={c}
          type="button"
          role="radio"
          aria-checked={value === c}
          aria-label={t(COLOR_NAME[c])}
          className={styles.color}
          style={{ background: HIGHLIGHT_FILL[c] }}
          onClick={() => onPick(c)}
        />
      ))}
    </div>
  )
}

export interface SelectionMenuProps {
  /** Where the text is on screen: the menu sits above it (below near the top). */
  rect: ScreenRect
  /** The tapped highlight's colour; none for a fresh selection. */
  current?: AnnotationColor | ''
  onHighlight: (c: AnnotationColor) => void
  onNote: () => void
  /** A tapped highlight: «Убрать выделение». */
  onRemove?: () => void
  onClose: () => void
  /** Focus its first button (the selection was made with the keyboard, or a highlight was opened). */
  autoFocus?: boolean
}

const MENU_H = 48

export function SelectionMenu({ rect, current, onHighlight, onNote, onRemove, onClose, autoFocus }: SelectionMenuProps) {
  const t = useT()
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (autoFocus) ref.current?.querySelector<HTMLButtonElement>('button')?.focus()
  }, [autoFocus])
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        e.stopPropagation()
        onClose()
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [onClose])
  const above = rect.y - MENU_H - 12 > 60
  const top = above ? rect.y - MENU_H - 10 : rect.y + rect.height + 10
  const left = Math.min(Math.max(rect.x + rect.width / 2, 150), window.innerWidth - 150)
  const move = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
    e.preventDefault()
    e.stopPropagation()
    const buttons = Array.from(ref.current?.querySelectorAll<HTMLButtonElement>('button') ?? [])
    const i = buttons.indexOf(document.activeElement as HTMLButtonElement)
    const next = buttons[(i + (e.key === 'ArrowRight' ? 1 : -1) + buttons.length) % buttons.length]
    next?.focus()
  }
  return (
    <div
      ref={ref}
      className={styles.menu}
      role="toolbar"
      aria-label={t('reader.selection')}
      style={{ top: Math.max(8, top), left }}
      onKeyDown={move}
      // a press here must not collapse the book's selection
      onPointerDown={(e) => e.preventDefault()}
      data-reader-chrome=""
    >
      {ANNOTATION_COLORS.map((c) => (
        <button
          key={c}
          type="button"
          className={styles.menuColor}
          aria-label={t('reader.highlight.as', { color: t(COLOR_NAME[c]) })}
          aria-pressed={current === c}
          style={{ background: HIGHLIGHT_FILL[c] }}
          onClick={() => onHighlight(c)}
        />
      ))}
      <span className={styles.menuSep} aria-hidden />
      <button type="button" className={styles.menuButton} onClick={onNote}>
        <Icon name="pencil" size={16} />
        {t('reader.note')}
      </button>
      {onRemove && (
        <button type="button" className={styles.menuButton} aria-label={t('reader.highlight.remove')} onClick={onRemove}>
          <Icon name="trash" size={16} />
        </button>
      )}
    </div>
  )
}
