// The profile's bookmarks, highlights and notes in one item (a book, an
// audiobook): this device's copy at once, then the server's merged in. A
// change shows here at once and syncs through offline/annotations; one the
// server refused is marked (`failed`) and announced for a moment (`notice`).
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { Annotation, AnnotationColor } from '../api/types'
import {
  cachedAnnotations,
  deleted,
  edited,
  failedAnnotations,
  liveAnnotations,
  makeAnnotation,
  refreshAnnotations,
  subscribeAnnotations,
  writeAnnotation,
  type NewAnnotation,
} from '../offline/annotations'
import { t } from '../i18n'

/** How long «Не удалось сохранить заметку» stays in the reader's bottom bar. */
export const NOTICE_MS = 5000

export interface AnnotationsApi {
  /** Not deleted, in reading order. */
  list: Annotation[]
  /** The ids the server refused: shown as not saved. */
  failed: Set<string>
  /** «Не удалось сохранить заметку» right after a refusal, else null. */
  notice: string | null
  add: (n: NewAnnotation) => Annotation
  update: (a: Annotation, patch: { note?: string; color?: AnnotationColor; kind?: Annotation['kind'] }) => void
  remove: (a: Annotation) => void
}

export function useAnnotations(itemId: number): AnnotationsApi {
  const [all, setAll] = useState<Annotation[]>(() => cachedAnnotations(itemId))
  const [failed, setFailed] = useState<Set<string>>(() => failedAnnotations(itemId))
  const [notice, setNotice] = useState<string | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null)
  const seenFailed = useRef(failed)

  useEffect(() => {
    const ac = new AbortController()
    const read = () => {
      setAll(cachedAnnotations(itemId))
      const now = failedAnnotations(itemId)
      if ([...now].some((id) => !seenFailed.current.has(id))) {
        setNotice(t('reader.annotations.notSaved'))
        if (timer.current) clearTimeout(timer.current)
        timer.current = setTimeout(() => setNotice(null), NOTICE_MS)
      }
      seenFailed.current = now
      setFailed(now)
    }
    read()
    const off = subscribeAnnotations(read)
    if (itemId > 0) {
      refreshAnnotations(itemId, ac.signal).catch((e: unknown) => {
        if (!ac.signal.aborted) console.warn('vault: annotations not loaded (this device’s copy shown)', e)
      })
    }
    return () => {
      ac.abort()
      off()
      if (timer.current) clearTimeout(timer.current)
    }
  }, [itemId])

  const add = useCallback(
    (n: NewAnnotation) => {
      const a = makeAnnotation(n)
      void writeAnnotation(itemId, a).catch(() => setAll((l) => [...l, a])) // not signed in: this page only
      return a
    },
    [itemId],
  )
  const update = useCallback(
    (a: Annotation, patch: { note?: string; color?: AnnotationColor; kind?: Annotation['kind'] }) => {
      void writeAnnotation(itemId, edited(a, patch)).catch(() => undefined)
    },
    [itemId],
  )
  const remove = useCallback(
    (a: Annotation) => {
      void writeAnnotation(itemId, deleted(a)).catch(() => undefined)
    },
    [itemId],
  )

  const list = useMemo(() => liveAnnotations(all), [all])
  return useMemo(() => ({ list, failed, notice, add, update, remove }), [list, failed, notice, add, update, remove])
}
