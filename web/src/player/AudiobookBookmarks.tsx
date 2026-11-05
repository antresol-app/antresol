// «Закладки» of an audiobook in «Сейчас играет»: a bookmark at the place
// playing now (`ms:N` from the start of the book), with an optional note,
// and the list of them — a pick jumps there. Synced like the reader's
// (useAnnotations).
import { useRef, useState } from 'react'
import { Icon } from '../components/Icon'
import { Sheet } from '../ui/Sheet'
import { useAnnotations } from '../reader/useAnnotations'
import type { Annotation } from '../api/types'
import { useT } from '../i18n'
import { bookPositionMs, chapterAt, seekAbsolute } from './audio/audiobook'
import { audio } from './audio/engine'
import { clock } from './timeline'
import styles from './AudiobookBookmarks.module.css'

/** `ms:N` → N, or -1. */
export function msOf(locator: string): number {
  const m = /^ms:(\d+)$/.exec(locator)
  return m ? Number(m[1]) : -1
}

export function AudiobookBookmarks({ bookId, className }: { bookId: number; className?: string }) {
  const t = useT()
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" className={className} aria-label={t('player.bookmarks')} onClick={() => setOpen(true)}>
        <Icon name="bookmark" size={20} />
      </button>
      {open && <BookmarksSheet bookId={bookId} onClose={() => setOpen(false)} />}
    </>
  )
}

function BookmarksSheet({ bookId, onClose }: { bookId: number; onClose: () => void }) {
  const t = useT()
  const notes = useAnnotations(bookId)
  // the place is taken when the sheet opens: it does not run on while a note is typed
  const at = useRef(bookPositionMs()).current
  const [note, setNote] = useState('')
  const marks = notes.list.filter((a) => a.kind === 'bookmark' && msOf(a.locator) >= 0)
  const add = () => {
    const s = audio.get()
    const it = s.queue[s.index]
    const ch = it ? chapterAt(it, s.positionSec) : null
    const durationMs = s.queue.reduce((sum, q) => sum + q.durationSec * 1000, 0)
    notes.add({ kind: 'bookmark', locator: `ms:${at}`, fraction: durationMs > 0 ? at / durationMs : 0, label: ch?.chapter.title ?? '', note: note.trim() })
    setNote('')
  }
  const jump = (a: Annotation) => {
    seekAbsolute(msOf(a.locator))
    onClose()
  }
  return (
    <Sheet
      title={t('player.bookmarks')}
      role="dialog"
      size="large"
      onCancel={onClose}
      closeButton
      actions={[{ label: t('player.bookmark.add'), kind: 'primary', run: add }]}
    >
      <label className={styles.label} htmlFor="audiobook-bookmark-note">
        {t('player.bookmark.at', { time: clock(at / 1000) })}
      </label>
      <input
        id="audiobook-bookmark-note"
        className={styles.field}
        value={note}
        maxLength={10000}
        placeholder={t('player.bookmark.note')}
        onChange={(e) => setNote(e.target.value)}
      />
      {marks.length === 0 ? (
        <p className={styles.empty}>{t('player.bookmark.none')}</p>
      ) : (
        <ul className={styles.list}>
          {marks.map((a) => {
            const time = clock(msOf(a.locator) / 1000)
            return (
              <li key={a.id} className={styles.row}>
                <button type="button" className={styles.jump} title={t('player.bookmark.goTo', { time })} onClick={() => jump(a)}>
                  <span className={styles.time}>{time}</span>
                  <span className={styles.text}>
                    {a.label && <span className={styles.where}>{a.label}</span>}
                    {a.note && <span className={styles.note}>{a.note}</span>}
                    {notes.failed.has(a.id) && (
                      <span className={styles.failed} role="status">
                        {t('reader.annotations.notSaved')}
                      </span>
                    )}
                  </span>
                </button>
                <button type="button" className={styles.icon} aria-label={t('reader.annotations.deleteOne', { name: time })} onClick={() => notes.remove(a)}>
                  <Icon name="trash" size={18} />
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </Sheet>
  )
}
