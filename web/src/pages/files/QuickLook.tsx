// Quick Look: a glass panel over the browser. Video and audio play, pictures
// and PDFs show, text, subtitles and .nfo open in a monospaced view. ← → (and
// ↑ ↓) go to the neighbouring files; Space or Esc closes.
import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { motion } from 'motion/react'
import { ApiError } from '../../api/client'
import { contentUrl, downloadFileUrl } from '../../api/files'
import { audio } from '../../player/audio/engine'
import type { FileEntry } from '../../api/types'
import { formatSize, kindLabel, objects } from './model'
import { FileIcon, folderEmblem } from './FileIcon'
import { Glyph } from './glyphs'
import { Thumb } from './Thumb'
import styles from './QuickLook.module.css'
import { useT } from '../../i18n'

export interface QuickLookProps {
  root: string
  /** Neighbours to page through, in view order. */
  entries: FileEntry[]
  path: string
  onNavigate: (path: string) => void
  onClose: () => void
  /** «Открыть в Vault» for files linked to a library item. */
  onOpenItem: (e: FileEntry) => void
  compact?: boolean
}

function TextPreview({ url }: { url: string }) {
  const t = useT()
  const [state, setState] = useState<{ text?: string; error?: string }>({})
  useEffect(() => {
    const ac = new AbortController()
    setState({})
    fetch(url, { signal: ac.signal, credentials: 'same-origin' })
      .then(async (r) => {
        if (!r.ok) {
          let msg = t('files.ql.openFailed')
          try {
            msg = ((await r.json()) as { error?: string }).error ?? msg
          } catch {
            /* plain text error */
          }
          throw new ApiError(r.status, msg)
        }
        return r.text()
      })
      .then(
        (text) => setState({ text }),
        (e: unknown) => {
          if (!ac.signal.aborted) setState({ error: e instanceof ApiError ? e.message : t('files.ql.openFailed') })
        },
      )
    return () => ac.abort()
  }, [url])
  if (state.error) return <p className={styles.message}>{state.error}</p>
  if (state.text === undefined) return <div className={styles.spinner} aria-label={t('common.loading')} />
  return (
    <pre className={styles.text} tabIndex={0}>
      {state.text}
    </pre>
  )
}

function Fallback({ entry, text, onOpenItem }: { entry: FileEntry; text?: string; onOpenItem?: () => void }) {
  const t = useT()
  return (
    <div className={styles.fallback}>
      <FileIcon entry={entry} size={160} emblem={folderEmblem(entry)} />
      {text && <p className={styles.message}>{text}</p>}
      {onOpenItem && (
        <button type="button" className={styles.pill} onClick={onOpenItem}>
          <Glyph name="play" size={14} /> {t('files.openInApp')}
        </button>
      )}
    </div>
  )
}

function Body({ root, entry, onOpenItem }: { root: string; entry: FileEntry; onOpenItem?: () => void }) {
  const t = useT()
  const [failed, setFailed] = useState(false)
  useEffect(() => setFailed(false), [entry.path])
  // Quick Look plays video and audio itself: the music steps aside.
  const media = !entry.isDir && (entry.kind === 'video' || entry.kind === 'audio')
  useEffect(() => {
    if (media) audio.yieldSession()
  }, [media, entry.path])
  const url = contentUrl(root, entry.path)
  if (entry.isDir) {
    return (
      <div className={styles.fallback}>
        <Thumb entry={entry} size={200} eager />
        <p className={styles.message}>{entry.childCount !== undefined ? objects(entry.childCount) : t('files.kind.folder')}</p>
      </div>
    )
  }
  if (failed) {
    return (
      <Fallback
        entry={entry}
        text={entry.kind === 'video' ? t('files.ql.format') : t('files.ql.openFailedDot')}
        onOpenItem={onOpenItem}
      />
    )
  }
  switch (entry.kind) {
    case 'video':
      return <video key={url} className={styles.media} src={url} controls autoPlay playsInline onError={() => setFailed(true)} />
    case 'audio':
      return (
        <div className={styles.fallback}>
          <Thumb entry={entry} size={180} eager />
          <audio key={url} src={url} controls autoPlay onError={() => setFailed(true)} className={styles.audio} />
        </div>
      )
    case 'image':
      return <img key={url} className={styles.image} src={url} alt={entry.name} onError={() => setFailed(true)} />
    case 'text':
    case 'subtitle':
      return <TextPreview url={url} />
    default:
      if (entry.name.toLowerCase().endsWith('.pdf')) return <iframe key={url} className={styles.pdf} src={url} title={entry.name} />
      return <Fallback entry={entry} text={t('files.ql.downloadOnly')} />
  }
}

export function QuickLook({ root, entries, path, onNavigate, onClose, onOpenItem, compact }: QuickLookProps) {
  const t = useT()
  const dialog = useRef<HTMLDivElement>(null)
  const idx = entries.findIndex((e) => e.path === path)
  const entry = entries[idx]
  const prev = idx > 0 ? entries[idx - 1] : undefined
  const next = idx >= 0 && idx < entries.length - 1 ? entries[idx + 1] : undefined

  const latest = useRef({ prev, next, onNavigate, onClose })
  latest.current = { prev, next, onNavigate, onClose }

  useEffect(() => {
    const restore = document.activeElement as HTMLElement | null
    dialog.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      const tg = e.target as HTMLElement
      const inMedia = tg.tagName === 'VIDEO' || tg.tagName === 'AUDIO'
      // A focused button or link keeps Space and Enter (‹ ›, Скачать, Открыть в Vault).
      const onControl = Boolean(tg.closest('button, a[href]')) && (e.key === ' ' || e.key === 'Enter')
      const l = latest.current
      if (onControl) return
      if (e.key === 'Escape' || (e.key === ' ' && !inMedia)) {
        e.preventDefault()
        e.stopPropagation()
        l.onClose()
      } else if ((e.key === 'ArrowLeft' || e.key === 'ArrowUp') && !inMedia) {
        e.preventDefault()
        e.stopPropagation()
        if (l.prev) l.onNavigate(l.prev.path)
      } else if ((e.key === 'ArrowRight' || e.key === 'ArrowDown') && !inMedia) {
        e.preventDefault()
        e.stopPropagation()
        if (l.next) l.onNavigate(l.next.path)
      } else if (e.key === 'Tab') {
        // Keep focus inside the panel.
        const els = dialog.current?.querySelectorAll<HTMLElement>('button, a[href], video, audio, [tabindex="0"]')
        if (!els?.length) return
        const first = els[0]!
        const last = els[els.length - 1]!
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault()
          last.focus()
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault()
          first.focus()
        }
      }
    }
    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      restore?.focus?.()
    }
  }, [])

  const touch = useRef<{ x: number; y: number } | null>(null)

  if (!entry) return null
  // Video and PDF get a large panel; everything else is sized to its content
  // (a short .srt stays small, a picture is never shown above its own size).
  const large = !entry.isDir && (entry.kind === 'video' || entry.name.toLowerCase().endsWith('.pdf'))
  const linked = entry.item && !entry.isDir ? () => onOpenItem(entry) : undefined
  const sub = entry.isDir ? t('files.kind.folder') : `${kindLabel(entry)} · ${formatSize(entry.size)}`

  return createPortal(
    <motion.div
      className={styles.backdrop}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      transition={{ duration: 0.18 }}
    >
      <motion.div
        ref={dialog}
        role="dialog"
        aria-modal="true"
        aria-label={t('files.ql.title', { name: entry.name })}
        tabIndex={-1}
        className={`${styles.panel} ${large ? styles.large : ''} ${compact ? styles.compact : ''} glass-strong`}
        initial={{ opacity: 0, scale: 0.94 }}
        animate={{ opacity: 1, scale: 1 }}
        transition={{ type: 'spring', stiffness: 520, damping: 38 }}
      >
        <header className={styles.bar}>
          <button type="button" className={styles.round} aria-label={t('common.close')} onClick={onClose}>
            <Glyph name="close" size={14} strokeWidth={2.4} />
          </button>
          <div className={styles.title}>
            <span className={styles.name}>{entry.name}</span>
            <span className={styles.sub}>{sub}</span>
          </div>
          <div className={styles.actions}>
            {linked && (
              <button type="button" className={styles.pill} onClick={linked}>
                {t('files.openInApp')}
              </button>
            )}
            {!entry.isDir && (
              <a className={styles.round} href={downloadFileUrl(root, entry.path)} download aria-label={t('files.download')} title={t('files.downloadShort')}>
                <Glyph name="arrowDownCircle" size={16} strokeWidth={2} />
              </a>
            )}
            <span className={styles.pager}>
              <button type="button" className={styles.round} aria-label={t('files.ql.prev')} disabled={!prev} onClick={() => prev && onNavigate(prev.path)}>
                <Glyph name="chevronLeft" size={15} strokeWidth={2.4} />
              </button>
              <button type="button" className={styles.round} aria-label={t('files.ql.next')} disabled={!next} onClick={() => next && onNavigate(next.path)}>
                <Glyph name="chevronRight" size={15} strokeWidth={2.4} />
              </button>
            </span>
          </div>
        </header>
        <div
          className={styles.body}
          // Swipe left / right pages through files on touch screens.
          onTouchStart={(e) => {
            const p = e.touches[0]
            touch.current = p ? { x: p.clientX, y: p.clientY } : null
          }}
          onTouchEnd={(e) => {
            const s = touch.current
            const p = e.changedTouches[0]
            touch.current = null
            const tag = (e.target as HTMLElement).tagName
            if (!s || !p || tag === 'VIDEO' || tag === 'AUDIO') return
            const dx = p.clientX - s.x
            const dy = p.clientY - s.y
            if (Math.abs(dx) < 60 || Math.abs(dx) < Math.abs(dy) * 1.5) return
            const to = dx < 0 ? next : prev
            if (to) onNavigate(to.path)
          }}
        >
          <Body key={entry.path} root={root} entry={entry} onOpenItem={linked} />
        </div>
      </motion.div>
    </motion.div>,
    document.body,
  )
}
