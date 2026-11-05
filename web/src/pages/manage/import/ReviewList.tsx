// Songs the import could not place on its own, each with the reason. The
// admin finds the album by hand («Найти вручную»: the server's music
// catalog search), picks one of the albums that hold the song («Выбрать
// альбом…») or leaves the song out («Пропустить»).
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { ApiError, getImportCandidates, getImportReview, imageUrl, resolveImportTrack, searchImport } from '../../../api/client'
import type { Candidate, ReviewTrack } from '../../../api/types'
import { Button } from '../../../ui/Button'
import { Icon } from '../../../components/Icon'
import { useCanDownload } from '../../../state/canDownload'
import { TextField } from '../../../ui/TextField'
import m from '../Manage.module.css'
import { t as tr, useT } from '../../../i18n'
import styles from './ReviewList.module.css'

const message = (e: unknown, fallback = tr('sources.failedRetry')) => (e instanceof ApiError ? e.message : fallback)

type Found = { status: 'idle' } | { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; candidates: Candidate[] }

function CandidateCard({ c, busy, onChoose }: { c: Candidate; busy: boolean; onChoose: () => void }) {
  const t = useT()
  const [failed, setFailed] = useState(false)
  const poster = imageUrl(c.poster, 160)
  return (
    <li className={styles.candidate}>
      <span className={styles.cover}>{poster && !failed ? <img src={poster} alt="" loading="lazy" onError={() => setFailed(true)} /> : <Icon name="music" size={22} />}</span>
      <span className={styles.candText}>
        <span className={styles.candTitle}>{c.matchedTrack ? t('import.reviewList.has', { title: c.title, track: c.matchedTrack }) : c.title}</span>
        <span className={styles.candMeta}>{[c.subtitle, c.year || ''].filter(Boolean).join(' · ')}</span>
      </span>
      <Button size="small" variant="secondary" loading={busy} disabled={busy || !c.extId} onClick={onChoose}>
        {t('import.reviewList.choose')}
      </Button>
    </li>
  )
}

function TrackRow({ track, onResolved }: { track: ReviewTrack; onResolved: (id: number) => void }) {
  const t = useT()
  // a server with no downloads has no music catalog to look in: only «Пропустить»
  const catalog = useCanDownload()
  const [mode, setMode] = useState<'' | 'search' | 'albums'>('')
  const [q, setQ] = useState(`${track.artist} ${track.title}`)
  const [found, setFound] = useState<Found>({ status: 'idle' })
  const [busy, setBusy] = useState<string>('') // extId being chosen, or 'skip'
  const [error, setError] = useState('')

  const fetchFound = (load: () => Promise<Candidate[]>) => {
    setFound({ status: 'loading' })
    load().then(
      (candidates) => setFound({ status: 'ready', candidates }),
      (e: unknown) => setFound({ status: 'error', message: message(e, tr('import.reviewList.searchFailed')) }),
    )
  }
  const search = (e: FormEvent) => {
    e.preventDefault()
    const query = q.trim()
    if (query) fetchFound(() => searchImport(query))
  }
  const albums = () => {
    setMode('albums')
    fetchFound(() => getImportCandidates(track.id))
  }
  const resolve = async (key: string, body: Parameters<typeof resolveImportTrack>[1]) => {
    setBusy(key)
    setError('')
    try {
      await resolveImportTrack(track.id, body)
      onResolved(track.id)
    } catch (e) {
      setError(message(e))
      setBusy('')
    }
  }
  const choose = (c: Candidate) =>
    void resolve(c.extId ?? '', { extId: c.extId, title: c.title, artist: c.subtitle ?? '', year: c.year, aliases: c.aliases ?? [] })

  return (
    <li className={styles.track} data-testid="review-track" data-track={track.id}>
      <div className={styles.head}>
        <div className={styles.text}>
          <p className={m.rowLabel}>
            {track.artist} — {track.title}
          </p>
          {track.album && <p className={m.rowHint}>{track.album}</p>}
          <p className={styles.reason}>{track.reason}</p>
        </div>
        <div className={styles.actions}>
          {catalog && (
            <>
              <Button size="small" variant={mode === 'search' ? 'primary' : 'secondary'} icon="search" disabled={busy !== ''} onClick={() => (setMode('search'), setFound({ status: 'idle' }))}>
                {t('import.reviewList.findManually')}
              </Button>
              <Button size="small" variant={mode === 'albums' ? 'primary' : 'secondary'} disabled={busy !== ''} onClick={albums}>
                {t('import.reviewList.chooseAlbum')}
              </Button>
            </>
          )}
          <Button size="small" variant="plain" loading={busy === 'skip'} disabled={busy !== ''} onClick={() => void resolve('skip', { skip: true })}>
            {t('import.reviewList.skip')}
          </Button>
        </div>
      </div>
      {mode === 'search' && (
        <form className={styles.search} onSubmit={search} role="search">
          <TextField label={t('import.reviewList.queryLabel')} hideLabel type="search" value={q} onChange={(e) => setQ(e.target.value)} autoFocus />
          <Button type="submit" size="small" disabled={!q.trim()}>
            {t('import.reviewList.find')}
          </Button>
        </form>
      )}
      {mode !== '' && found.status === 'loading' && (
        <p className={styles.note} role="status">
          <span className={m.spinner} aria-hidden /> {t('import.reviewList.searching')}
        </p>
      )}
      {mode !== '' && found.status === 'error' && (
        <p className={styles.error} role="alert">
          {found.message}
        </p>
      )}
      {mode !== '' && found.status === 'ready' &&
        (found.candidates.length === 0 ? (
          <p className={styles.note}>{t('import.reviewList.nothing')}</p>
        ) : (
          <ul className={styles.candidates} aria-label={t('common.albums')}>
            {found.candidates.map((c) => (
              <CandidateCard key={c.extId ?? c.title} c={c} busy={busy === c.extId} onChoose={() => choose(c)} />
            ))}
          </ul>
        ))}
      {error && (
        <p className={styles.error} role="alert">
          {error}
        </p>
      )}
    </li>
  )
}

export interface ReviewListProps {
  jobId: number
  /** A song was resolved or skipped (the job's counts changed). */
  onChanged: () => void
}

export function ReviewList({ jobId, onChanged }: ReviewListProps) {
  const t = useT()
  const [tracks, setTracks] = useState<ReviewTrack[] | null>(null)
  const [error, setError] = useState('')

  const load = useCallback(() => {
    setError('')
    getImportReview(jobId).then(setTracks, (e: unknown) => setError(message(e, tr('import.reviewList.loadFailed'))))
  }, [jobId])
  useEffect(load, [load])

  const listRef = useRef<HTMLUListElement>(null)
  const doneRef = useRef<HTMLParagraphElement>(null)
  /** Where focus goes once the decided song's row is gone: the next song (or the previous), else «Все песни разобраны.». */
  const [focusNext, setFocusNext] = useState<number | 'done' | null>(null)
  useEffect(() => {
    if (focusNext === null) return
    if (focusNext === 'done') doneRef.current?.focus()
    else listRef.current?.querySelector<HTMLElement>(`[data-track="${focusNext}"] button`)?.focus()
    setFocusNext(null)
  }, [focusNext, tracks])

  const resolved = (id: number) => {
    const list = tracks ?? []
    const i = list.findIndex((x) => x.id === id)
    const next = list[i + 1] ?? list[i - 1]
    setTracks((ts) => ts?.filter((x) => x.id !== id) ?? ts)
    setFocusNext(next ? next.id : 'done')
    onChanged()
  }

  if (error)
    return (
      <div className={m.problem} role="alert">
        <Icon name="warning" size={18} /> {error}
        <Button size="small" variant="secondary" onClick={load}>
          {t('common.retry')}
        </Button>
      </div>
    )
  if (!tracks)
    return (
      <div className={m.spinnerRow} role="status">
        <span className={m.spinner} aria-hidden /> {t('import.reviewList.loading')}
      </div>
    )
  if (tracks.length === 0)
    return (
      <p ref={doneRef} className={styles.note} role="status" tabIndex={-1}>
        {t('import.reviewList.allDone')}
      </p>
    )
  return (
    <ul ref={listRef} className={styles.tracks} aria-label={t('import.reviewList.aria')}>
      {tracks.map((track) => (
        <TrackRow key={track.id} track={track} onResolved={resolved} />
      ))}
    </ul>
  )
}
