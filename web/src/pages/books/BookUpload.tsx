// «Добавить книги»: files from this device (the picker, or dropped anywhere on
// «Книги») go to the server one by one; the server indexes each and makes it
// this profile's. One request per file: a proxy limit then costs one book, not
// the whole batch, and the line below can say how far it got.
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ApiError, uploadBook } from '../../api/client'
import { Button } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { Icon } from '../../components/Icon'
import { useT } from '../../i18n'
import { useIsCompact } from '../../state/useMediaQuery'
import styles from './BooksHome.module.css'

const carriesFiles = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes('Files')

export function useBookUpload(onUploaded: () => void) {
  const t = useT()
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null)
  const [result, setResult] = useState<{ added: number; existing: number; failed: string[] } | null>(null)
  const busy = useRef(false)

  const upload = async (files: File[]) => {
    if (busy.current || files.length === 0) return
    busy.current = true
    setResult(null)
    const out = { added: 0, existing: 0, failed: [] as string[] }
    for (const [i, f] of files.entries()) {
      setProgress({ done: i, total: files.length })
      try {
        const r = await uploadBook(f)
        if (r.duplicate) out.existing++
        else out.added++
      } catch (e) {
        // a proxy's refusal has no message of ours
        const why = e instanceof ApiError && e.status === 413 ? t('books.upload.tooLarge') : e instanceof ApiError ? e.message : t('books.upload.failed')
        out.failed.push(`${f.name}: ${why}`)
      }
    }
    setProgress(null)
    setResult(out)
    busy.current = false
    if (out.added + out.existing > 0) onUploaded()
  }
  return { upload, progress, result }
}

export function BookUploadButton({ onFiles, busy }: { onFiles: (files: File[]) => void; busy: boolean }) {
  const t = useT()
  const compact = useIsCompact()
  const input = useRef<HTMLInputElement>(null)
  const open = () => input.current?.click()
  return (
    <>
      {/* no accept: phones grey out .fb2, the server checks every file */}
      <input
        ref={input}
        type="file"
        multiple
        hidden
        data-testid="book-upload-input"
        onChange={(e) => {
          onFiles(Array.from(e.target.files ?? []))
          e.target.value = ''
        }}
      />
      {compact ? (
        <IconButton icon="plus" label={t('books.upload.add')} loading={busy} onClick={open} />
      ) : (
        <Button variant="secondary" size="small" icon="plus" loading={busy} onClick={open}>
          {t('books.upload.add')}
        </Button>
      )}
    </>
  )
}

export function BookUploadStatus({ progress, result }: Pick<ReturnType<typeof useBookUpload>, 'progress' | 'result'>) {
  const t = useT()
  if (progress) {
    return (
      <p className={styles.uploadStatus} role="status">
        {t('books.upload.progress', { n: progress.done + 1, total: progress.total })}
      </p>
    )
  }
  if (!result) return null
  const lines: string[] = []
  if (result.added) lines.push(t('books.upload.added', { count: result.added }))
  if (result.existing) lines.push(t('books.upload.existing', { count: result.existing }))
  return (
    <>
      {lines.length > 0 && (
        <p className={styles.uploadStatus} role="status">
          {lines.join(' ')}
        </p>
      )}
      {result.failed.map((f) => (
        <p key={f} className={styles.uploadError} role="alert">
          {f}
        </p>
      ))}
    </>
  )
}

/** Files dropped anywhere on the page; shows a veil while they are over it. */
export function BookDropZone({ enabled, onFiles, children }: { enabled: boolean; onFiles: (files: File[]) => void; children: ReactNode }) {
  const t = useT()
  const [over, setOver] = useState(false)
  const depth = useRef(0)
  const latest = useRef(onFiles)
  latest.current = onFiles
  useEffect(() => {
    if (!enabled) return
    const enter = (e: DragEvent) => {
      if (!carriesFiles(e)) return
      e.preventDefault()
      depth.current++
      setOver(true)
    }
    const dragOver = (e: DragEvent) => {
      if (carriesFiles(e)) e.preventDefault() // else the browser opens the file
    }
    const leave = (e: DragEvent) => {
      if (!carriesFiles(e)) return
      depth.current = Math.max(0, depth.current - 1)
      if (depth.current === 0) setOver(false)
    }
    const drop = (e: DragEvent) => {
      if (!carriesFiles(e)) return
      e.preventDefault()
      depth.current = 0
      setOver(false)
      latest.current(Array.from(e.dataTransfer?.files ?? []))
    }
    window.addEventListener('dragenter', enter)
    window.addEventListener('dragover', dragOver)
    window.addEventListener('dragleave', leave)
    window.addEventListener('drop', drop)
    return () => {
      window.removeEventListener('dragenter', enter)
      window.removeEventListener('dragover', dragOver)
      window.removeEventListener('dragleave', leave)
      window.removeEventListener('drop', drop)
    }
  }, [enabled])
  return (
    <>
      {children}
      {over && (
        <div className={styles.dropVeil} aria-hidden>
          <Icon name="book" size={48} strokeWidth={1.4} />
          <p>{t('books.upload.drop')}</p>
        </div>
      )}
    </>
  )
}
