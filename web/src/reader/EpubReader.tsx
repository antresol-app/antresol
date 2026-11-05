// EPUB, FB2 (also .fb2.zip), MOBI and AZW3 through foliate-js: paginated
// text in the reader's theme, font and size; pages turn with ←/→, PageUp/
// PageDown, Space, a click on the left or right third and a swipe. The
// position (a CFI) is saved as you read and restored on the next open.
import { useCallback, useEffect, useRef, useState } from 'react'
import { annotationsExportUrl, downloadUrl } from '../api/client'
import type { Annotation, AnnotationColor, BookDetail } from '../api/types'
import { Button, ButtonLink } from '../ui/Button'
import { Icon } from '../components/Icon'
import { FamilyMarks, ReturnPill } from '../components/FamilyMarks'
import type { FamilyPlace } from '../player/familyMarks'
import { useIsCompact, useReducedMotion } from '../state/useMediaQuery'
import type { RelocateDetail, TocItem, View } from '../vendor/foliate/view.js'
import * as CFI from '../vendor/foliate/epubcfi.js'
import { NoteSheet, SelectionMenu } from './Annotations'
import { HighlightLayer, readSelection, type ScreenRect, type TextSelection } from './highlights'
import { marksOnPage } from './native/protocol'
import { useAnnotations } from './useAnnotations'
import { loadPrefs, savePrefs, THEMES, type ReaderPrefs } from './prefs'
import { applyReaderStyle } from './readerStyle'
import { chromeKey, inChrome, ReaderChrome, useAutoHide, type SearchHit, type TocEntry } from './ReaderChrome'
import { guardBook } from './sanitize'
import { useReaderFamily } from './useReaderFamily'
import { useReadingProgress, type ReadingPosition } from './useReadingProgress'
import styles from './Reader.module.css'
import { t as translate, useT } from '../i18n'

export interface BookReaderProps {
  book: BookDetail
  file: File
  /** Leaves the reader (after the position is saved). */
  onExit: () => void
}

function flattenToc(items: TocItem[] | undefined, depth = 0, out: TocEntry[] = []): TocEntry[] {
  for (const it of items ?? []) {
    out.push({ label: (it.label ?? '').trim() || translate('common.untitled'), href: it.href, depth })
    if (it.subitems?.length) flattenToc(it.subitems, depth + 1, out)
  }
  return out
}

/** Keys that turn pages; the rest are left to the page. */
export function pageKey(e: KeyboardEvent): -1 | 1 | 0 | 'left' | 'right' {
  if (e.altKey || e.ctrlKey || e.metaKey) return 0
  switch (e.key) {
    case 'ArrowLeft':
      return 'left'
    case 'ArrowRight':
      return 'right'
    case 'PageUp':
      return -1
    case 'PageDown':
      return 1
    case ' ':
      return e.shiftKey ? -1 : 1
  }
  return 0
}

/** True when a key press belongs to a field, a menu or a dialog. */
function ownedElsewhere(e: Event): boolean {
  const t = e.target
  if (!(t instanceof Element)) return false
  return !!t.closest('input, textarea, select, [contenteditable="true"], [role="menu"], [role="toolbar"], [role="dialog"], [role="alertdialog"]')
}

type Status = { status: 'loading' } | { status: 'ready' } | { status: 'error'; message: string }

/** The menu over the text: a fresh selection (in `doc`), or a highlight that was tapped. */
type Menu = { sel: TextSelection; doc: Document; keyboard: boolean } | { annotation: Annotation; rect: ScreenRect }
/** The note sheet: of an annotation, or of a selection that becomes one. */
type NoteEdit = { annotation: Annotation } | { sel: TextSelection; doc: Document; color: AnnotationColor }

/** The bookmarks of a book (CFI points). */
const bookmarkLocators = (list: Annotation[]) => list.filter((a) => a.kind === 'bookmark' && a.locator.startsWith('epubcfi(')).map((a) => a.locator)

export function EpubReader({ book, file, onExit }: BookReaderProps) {
  const t = useT()
  const compact = useIsCompact()
  const reduceMotion = useReducedMotion()
  const hostRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<View | null>(null)
  const [prefs, setPrefs] = useState<ReaderPrefs>(loadPrefs)
  const prefsRef = useRef(prefs)
  prefsRef.current = prefs
  const [status, setStatus] = useState<Status>({ status: 'loading' })
  // pos: where the reader is (panels); savePos: the same, but only once the
  // reader moved it (opening a book is not reading it)
  const [pos, setPos] = useState<ReadingPosition | null>(null)
  const [savePos, setSavePos] = useState<ReadingPosition | null>(null)
  const moved = useRef(false)
  const searchRun = useRef(0)
  const [chapter, setChapter] = useState('')
  const [currentHref, setCurrentHref] = useState<string>()
  const [toc, setToc] = useState<TocEntry[]>([])
  const [locked, setLocked] = useState(false)
  // the panels stay up over a loading or failed book: «‹ Книги» is the way out
  const chrome = useAutoHide(locked || status.status !== 'ready')
  const chromeRef = useRef(chrome)
  chromeRef.current = chrome
  const initial = book.progress?.locator ? { locator: book.progress.locator, fraction: book.progress.fraction } : null
  const { flush, error: saveError, hold } = useReadingProgress(book.card.id, savePos, initial)
  const family = useReaderFamily<ReadingPosition>(book.card.id)
  const notes = useAnnotations(book.card.id)
  const notesRef = useRef(notes)
  notesRef.current = notes
  const layerRef = useRef<HighlightLayer | null>(null)
  const [menu, setMenu] = useState<Menu | null>(null)
  const [noteEdit, setNoteEdit] = useState<NoteEdit | null>(null)
  const familyRef = useRef(family)
  familyRef.current = family
  /** The reader turned a page (or went somewhere) itself: a held jump is now where it reads. */
  const readOn = useCallback(() => {
    moved.current = true
    hold(false)
  }, [hold])
  const readOnRef = useRef(readOn)
  readOnRef.current = readOn

  const exit = useCallback(() => {
    void flush(false).finally(onExit)
  }, [flush, onExit])
  const exitRef = useRef(exit)
  exitRef.current = exit

  const apply = useCallback(
    (view: View, p: ReaderPrefs) => {
      applyReaderStyle(view.renderer, p, { compact, reduceMotion })
    },
    [compact, reduceMotion],
  )

  /** A click at window x: left/right third turns the page, the middle shows the panels. */
  const tap = useCallback((x: number) => {
    const v = viewRef.current
    if (!v) return
    const w = window.innerWidth
    if (x < w / 3) void v.goLeft()
    else if (x > (2 * w) / 3) void v.goRight()
    else return chromeRef.current.toggle()
    readOnRef.current()
    chromeRef.current.hide()
  }, [])

  const onKey = useCallback((e: KeyboardEvent) => {
    if (e.defaultPrevented || ownedElsewhere(e)) return
    const v = viewRef.current
    const k = pageKey(e)
    if (k === 0 || !v) {
      chromeKey(e, chromeRef.current, exitRef.current)
      return
    }
    // Space on a control of the panels presses it
    if (e.key === ' ' && inChrome(e)) return
    e.preventDefault()
    chromeRef.current.hide()
    readOnRef.current()
    if (k === 'left') void v.goLeft()
    else if (k === 'right') void v.goRight()
    else if (k === 1) void v.next()
    else void v.prev()
  }, [])

  // Open the book once per file.
  useEffect(() => {
    let cancelled = false
    let view: View | null = null
    const host = hostRef.current
    setStatus({ status: 'loading' })
    ;(async () => {
      await import('../vendor/foliate/view.js')
      if (cancelled || !host) return
      view = document.createElement('foliate-view') as View
      view.className = styles.view ?? ''
      view.addEventListener('relocate', (e) => {
        const d = (e as CustomEvent<RelocateDetail>).detail
        const p = { locator: d.cfi, fraction: d.fraction ?? 0 }
        setPos(p)
        if (moved.current) setSavePos(p)
        setChapter(d.tocItem?.label?.trim() ?? '')
        setCurrentHref(d.tocItem?.href)
      })
      view.addEventListener('load', (e) => {
        const doc = (e as CustomEvent<{ doc: Document }>).detail.doc
        doc.addEventListener('keydown', onKey)
        // a swipe turns the page (foliate's own touch handling)
        doc.addEventListener('touchmove', () => readOnRef.current(), { passive: true })
        doc.addEventListener('click', (ev) => {
          if (ev.defaultPrevented || ev.button !== 0) return // links are foliate's
          if (doc.getSelection()?.toString()) return
          const frame = doc.defaultView?.frameElement
          const left = frame ? frame.getBoundingClientRect().left : 0
          // the highlight's own click handler runs after this one: a tap on a highlight opens its menu instead
          setTimeout(() => {
            if (!layerRef.current?.justTapped()) tap(left + ev.clientX)
          }, 0)
        })
        // a selection made (with the pointer or Shift+arrows) gets the «Выделить» menu
        const index = (e as CustomEvent<{ index: number }>).detail.index
        const selected = (ev: Event) =>
          setTimeout(() => {
            const v = view
            if (!v) return
            const sel = readSelection(v, doc, index)
            if (sel) setMenu({ sel, doc, keyboard: ev.type === 'keyup' })
          }, 0)
        doc.addEventListener('pointerup', selected)
        doc.addEventListener('keyup', (ev) => {
          if (ev.shiftKey || ev.key === 'Shift') selected(ev)
        })
        // a phone's long press and its handles end in no pointerup: the selection settling does it
        let settle: ReturnType<typeof setTimeout> | undefined
        doc.addEventListener('selectionchange', () => {
          clearTimeout(settle)
          if (doc.getSelection()?.isCollapsed) setMenu((m) => (m && 'sel' in m && m.doc === doc ? null : m))
          else settle = setTimeout(() => selected(new Event('selectionchange')), 400)
        })
      })
      layerRef.current = new HighlightLayer(view, (id, _locator, rect) => {
        const a = notesRef.current.list.find((x) => x.id === id)
        if (a) setMenu({ annotation: a, rect: rect ?? { x: window.innerWidth / 2, y: window.innerHeight / 2, width: 0, height: 0 } })
      })
      host.append(view)
      // book content is untrusted: no scripts (the CSP blocks them too)
      const { makeBook } = await import('../vendor/foliate/view.js')
      const parsed = await makeBook(file)
      guardBook(parsed)
      if (cancelled) return
      await view.open(parsed)
      if (cancelled) return
      apply(view, prefsRef.current)
      setToc(flattenToc(view.book.toc))
      try {
        await view.init({ lastLocation: book.progress?.locator || null })
      } catch (err) {
        // a position from another device this file cannot resolve: open at the start
        console.warn('saved position not found', err)
        await view.init({})
      }
      if (cancelled) return
      viewRef.current = view
      setStatus({ status: 'ready' })
    })().catch((err: unknown) => {
      if (cancelled) return
      console.error(err)
      setStatus({ status: 'error', message: t('reader.epubBroken') })
    })
    return () => {
      cancelled = true
      viewRef.current = null
      layerRef.current?.destroy()
      layerRef.current = null
      try {
        view?.close()
      } catch {
        /* never opened */
      }
      view?.remove()
    }
    // book.progress, prefs and the handlers are read once, at open
  }, [file])

  useEffect(() => {
    const v = viewRef.current
    if (v && status.status === 'ready') apply(v, prefs)
  }, [prefs, apply, status.status])

  // the highlights and notes drawn in the book
  const highlights = notes.list.filter((a) => a.kind !== 'bookmark')
  const highlightKey = highlights.map((a) => `${a.id}|${a.locator}|${a.color}|${a.kind}`).join('\n')
  useEffect(() => {
    if (status.status !== 'ready') return
    layerRef.current?.set(highlights.map((a) => ({ id: a.id, locator: a.locator, color: a.color, note: a.kind === 'note' })))
    // the list's identity changes with every edit; what is drawn only with these
  }, [highlightKey, status.status])

  // the bookmarks on the page on screen
  const onPage = pos ? marksOnPage(pos.locator, bookmarkLocators(notes.list)) : []
  const toggleBookmark = () => {
    if (!pos) return
    if (onPage.length > 0) {
      for (const a of notes.list) if (a.kind === 'bookmark' && onPage.includes(a.locator)) notes.remove(a)
      return
    }
    let at = pos.locator
    try {
      at = CFI.collapse(pos.locator)
    } catch {
      /* a point already */
    }
    notes.add({ kind: 'bookmark', locator: at, fraction: pos.fraction, label: chapter })
  }

  const clearSelection = (doc: Document) => doc.getSelection()?.removeAllRanges()
  const highlightSelection = (m: Menu, color: AnnotationColor) => {
    if ('sel' in m) {
      notes.add({ kind: 'highlight', locator: m.sel.cfi, fraction: pos?.fraction ?? 0, label: chapter, excerpt: m.sel.text, color })
      clearSelection(m.doc)
    } else notes.update(m.annotation, { color })
    setMenu(null)
  }
  const saveNote = (edit: NoteEdit, note: string, color: AnnotationColor) => {
    const kind = note ? 'note' : 'highlight'
    if ('annotation' in edit) notes.update(edit.annotation, { note, color, kind })
    else {
      notes.add({ kind, locator: edit.sel.cfi, fraction: pos?.fraction ?? 0, label: chapter, excerpt: edit.sel.text, note, color })
      clearSelection(edit.doc)
    }
    setNoteEdit(null)
  }
  const jumpTo = (a: Annotation) => {
    readOn()
    family.ret.clear()
    void viewRef.current?.goTo(a.locator)
  }

  useEffect(() => {
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onKey])

  const changePrefs = (p: ReaderPrefs) => {
    setPrefs(p)
    savePrefs(p)
  }

  const search = useCallback(async (query: string): Promise<SearchHit[]> => {
    const v = viewRef.current
    if (!v) return []
    const run = ++searchRun.current
    const hits: SearchHit[] = []
    for await (const r of v.search({ query })) {
      if (r === 'done' || run !== searchRun.current) break // done, or the sheet closed
      if ('subitems' in r) for (const s of r.subitems) hits.push({ cfi: s.cfi, label: r.label, ...s.excerpt })
      if (hits.length >= 300) break
    }
    return hits
  }, [])

  // «Семья»: the other readers on the book's line; a jump is only looked at until the reader reads on
  const places: FamilyPlace[] = family.members.map((m) => ({
    member: m,
    at: Math.min(1, Math.max(0, m.fraction)),
    label: m.page ? translate('comp.family.page', { n: m.page }) : translate('comp.family.percent', { n: Math.round(m.fraction * 100) }),
  }))
  const jumpToPlace = (pl: FamilyPlace) => {
    const v = viewRef.current
    if (!v) return
    const mine = family.ret.back?.to ?? pos
    hold(true)
    if (mine) family.ret.offer(mine, translate('comp.family.percent', { n: Math.round(mine.fraction * 100) }))
    const loc = pl.member.locator ?? ''
    void (loc.startsWith('epubcfi(') ? v.goTo(loc) : v.goToFraction(pl.at))
    chrome.poke()
  }
  const returnBack = () => {
    const back = family.ret.back
    if (!back) return
    family.ret.clear()
    hold(false)
    void viewRef.current?.goTo(back.to.locator)
  }

  const theme = THEMES[prefs.theme]
  return (
    <div className={styles.reader} style={{ background: theme.bg, color: theme.fg }} data-theme={prefs.theme} data-testid="reader" data-locator={pos?.locator} data-ready={status.status === 'ready' || undefined}>
      <div
        ref={hostRef}
        className={styles.host}
        onClick={(e) => {
          // the margins around the page (outside the book's iframe)
          if (e.target === e.currentTarget) tap(e.clientX)
        }}
      />
      {status.status === 'loading' && (
        <div className={styles.overlay} aria-busy="true" aria-label={t('reader.openingBook')}>
          <span className={styles.spinner} />
        </div>
      )}
      {status.status === 'error' && (
        <div className={styles.overlay} role="alert">
          <Icon name="book" size={44} strokeWidth={1.4} />
          <h2>{t('books.book.openFailed')}</h2>
          <p>{status.message}</p>
          <div className={styles.row}>
            <Button variant="secondary" onClick={exit}>
              {t('common.back')}
            </Button>
            {book.readFileId > 0 && (
              <ButtonLink to={downloadUrl(book.readFileId)} reloadDocument variant="secondary" icon="download">
                {t('reader.downloadFile')}
              </ButtonLink>
            )}
          </div>
        </div>
      )}
      <ReaderChrome
        title={book.card.title}
        visible={chrome.visible}
        theme={prefs.theme}
        onBack={exit}
        chapter={chapter}
        fraction={pos ? pos.fraction : null}
        onLockChange={setLocked}
        toc={toc}
        currentHref={currentHref}
        onToc={(href) => {
          readOn()
          family.ret.clear()
          void viewRef.current?.goTo(href)
        }}
        prefs={prefs}
        onPrefs={changePrefs}
        search={search}
        onSearchPick={(hit) => {
          readOn()
          family.ret.clear()
          void viewRef.current?.goTo(hit.cfi)
        }}
        onSearchClose={(picked) => {
          searchRun.current++
          if (!picked) viewRef.current?.clearSearch()
        }}
        note={saveError ?? notes.notice}
        bookmarked={onPage.length > 0}
        onBookmark={status.status === 'ready' ? toggleBookmark : undefined}
        annotations={{
          list: notes.list,
          highlights: true,
          onJump: jumpTo,
          onRemove: notes.remove,
          onEditNote: (a) => setNoteEdit({ annotation: a }),
          exportUrl: annotationsExportUrl(book.card.id),
          failed: notes.failed,
        }}
        family={places.length > 0 && status.status === 'ready' && <FamilyMarks places={places} total={1} onJump={jumpToPlace} />}
      />
      {menu && (
        <SelectionMenu
          rect={'sel' in menu ? menu.sel.rect : menu.rect}
          current={'annotation' in menu ? menu.annotation.color : undefined}
          autoFocus={'annotation' in menu || menu.keyboard}
          onHighlight={(c) => highlightSelection(menu, c)}
          onNote={() => {
            setNoteEdit('sel' in menu ? { sel: menu.sel, doc: menu.doc, color: 'yellow' } : { annotation: menu.annotation })
            setMenu(null)
          }}
          onRemove={
            'annotation' in menu
              ? () => {
                  notes.remove(menu.annotation)
                  setMenu(null)
                }
              : undefined
          }
          onClose={() => {
            if ('sel' in menu) clearSelection(menu.doc)
            setMenu(null)
          }}
        />
      )}
      {noteEdit && (
        <NoteSheet
          excerpt={'annotation' in noteEdit ? noteEdit.annotation.excerpt : noteEdit.sel.text}
          note={'annotation' in noteEdit ? noteEdit.annotation.note : ''}
          color={(('annotation' in noteEdit ? noteEdit.annotation.color : noteEdit.color) || 'yellow') as AnnotationColor}
          onSave={(note, color) => saveNote(noteEdit, note, color)}
          onClose={() => setNoteEdit(null)}
        />
      )}
      {family.ret.back && (
        <ReturnPill className={styles.returnPill} label={family.ret.back.label} onReturn={returnBack} onDismiss={family.ret.clear} />
      )}
    </div>
  )
}
