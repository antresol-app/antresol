// «Импорт» (admin): songs, playlists and «Избранное» from files, once, in
// both editions. In this order:
//   1. the way in: upload a file (CSV, M3U, Library.xml, a Takeout zip). The
//      sources build shows a tile per streaming service instead (./import/Services:
//      sign-ins, a pasted token, a public playlist), loaded lazily; the
//      core build's bundle has none of it;
//   2. the preview: what would come, «Избранное» on or off, which playlists
//      (and, where the server downloads, the size against the free space) —
//      «Начать импорт»;
//   3. the progress: songs ready, the albums, the songs that need a decision
//      («Нужно ваше решение») and what could not be done. Where nothing is
//      downloaded the albums the library has are linked and the rest are
//      «Нет в медиатеке».
import { APP_NAME } from '../../brand'
import { lazy, Suspense, useEffect, useState } from 'react'
import { useSearchParams } from 'react-router'
import {
  ApiError,
  cancelImportJob,
  getImportJobs,
  patchImportJob,
  retryImportGroup,
  startImportJob,
  uploadImportFiles,
} from '../../api/client'
import type { ImportJob, ImportJobPatch } from '../../api/types'
import { Button } from '../../ui/Button'
import { Icon } from '../../components/Icon'
import { Sheet } from '../../ui/Sheet'
import { FileImport } from './import/FileImport'
import { ImportPreview } from './import/ImportPreview'
import { ImportProgress } from './import/ImportProgress'
import { ReviewList } from './import/ReviewList'
import { serviceName } from './import/serviceName'
import { isActive, isOpen, useImportJob } from './import/useImportJob'
import styles from './Import.module.css'
import m from './Manage.module.css'
import { LANGS, t as tr, translate, useT, type Key } from '../../i18n'
import { useCanDownload } from '../../state/canDownload'

/** The sources build's service tiles and sign-ins; the core build has none. */
const Services = __LIBRARY_EDITION__ ? null : lazy(() => import('./import/Services'))

const errorText = (e: unknown, fallback = tr('sources.failedRetry')) => (e instanceof ApiError ? e.message : fallback)
/** What a cancel keeps (said before and after it). */
const kept = (downloads: boolean) => tr(downloads ? 'import.kept' : 'import.keptLocal')

function Header({ downloads }: { downloads: boolean }) {
  const t = useT()
  return (
    <header className={m.header}>
      <div>
        <h2 id="import-title" className={m.title}>
          {t('import.title')}
        </h2>
        <p className={`${m.subtitle} ${styles.intro}`}>{t(downloads ? 'import.intro' : 'import.introLocal')}</p>
      </div>
    </header>
  )
}

type Busy = '' | 'upload' | 'start' | 'cancel'

/**
 * What the sign-in callback may say (?error=, in either language); any other
 * text is not shown: the address can be made up. Said in the interface language.
 */
const SIGN_IN_ERRORS: Key[] = ['import.connectFailed', 'import.signIn.expired', 'import.signIn.notConfigured', 'import.signIn.unknown', 'import.signIn.running']
const signInError = (e: string | null) => {
  if (!e) return ''
  const key = SIGN_IN_ERRORS.find((k) => LANGS.some((l) => translate(l, k) === e))
  return tr(key ?? 'import.connectFailed')
}

export function Import() {
  const t = useT()
  const downloads = useCanDownload()
  // back from a sign-in: ?job=<the job reading the library> or ?error=<why not>
  const [params, setParams] = useSearchParams()
  const [back] = useState(() => ({ job: Number(params.get('job')) || undefined, error: signInError(params.get('error')) }))
  const { services, job, groups, loaded, error, stale, load, refresh, show, dismiss } = useImportJob(back.job)
  const [busy, setBusy] = useState<Busy>('')
  const [problem, setProblem] = useState(back.error)
  /** A one-line outcome over the tiles («Импорт отменён…»). */
  const [notice, setNotice] = useState('')
  /** A new upload hit an open job of the same kind: the files wait for the choice. */
  const [conflict, setConflict] = useState<{ files: File[]; open: ImportJob | null } | null>(null)
  const [confirmCancel, setConfirmCancel] = useState(false)
  const [reviewing, setReviewing] = useState(false)

  useEffect(() => {
    document.title = `${tr('import.short')} — ${APP_NAME}`
  }, [])
  // read once: a reload does not repeat the message
  useEffect(() => {
    if (params.has('job') || params.has('error')) setParams({}, { replace: true })
  }, [])

  const upload = async (files: File[]) => {
    setProblem('')
    setNotice('')
    setBusy('upload')
    try {
      show(await uploadImportFiles(files))
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        const jobs = await getImportJobs().catch(() => [] as ImportJob[])
        const open = [...jobs].sort((a, b) => Number(isActive(b)) - Number(isActive(a)) || b.id - a.id).find(isOpen) ?? null
        setConflict({ files, open })
      } else setProblem(errorText(e, t('import.readFileFailed')))
    } finally {
      setBusy('')
    }
  }

  const continueOpen = () => {
    const open = conflict?.open
    setConflict(null)
    if (open) show(open)
    else void load()
  }
  const cancelOpenAndUpload = async () => {
    const c = conflict
    if (!c) return
    setBusy('cancel')
    try {
      if (c.open) await cancelImportJob(c.open.id)
      setConflict(null)
      if (c.open && isActive(c.open)) setNotice(`${t('import.canceled')} ${kept(downloads)}`)
    } catch (e) {
      setConflict(null)
      setProblem(errorText(e))
      setBusy('')
      return
    }
    setBusy('')
    await upload(c.files)
  }

  const patch = async (p: ImportJobPatch) => {
    if (!job) return
    setProblem('')
    // the switch and the boxes move at once; the counts follow the answer
    show({
      ...job,
      favorites: p.favorites ?? job.favorites,
      playlists: job.playlists.map((pl) => (p.playlists && String(pl.id) in p.playlists ? { ...pl, selected: p.playlists[String(pl.id)]! } : pl)),
    })
    try {
      show(await patchImportJob(job.id, p))
    } catch (e) {
      setProblem(errorText(e))
      void refresh()
    }
  }
  const start = async () => {
    if (!job) return
    setProblem('')
    setBusy('start')
    try {
      await startImportJob(job.id)
      show({ ...job, status: 'matching' })
      void refresh()
    } catch (e) {
      setProblem(errorText(e))
      void refresh()
    } finally {
      setBusy('')
    }
  }
  const cancel = async () => {
    if (!job) return
    setBusy('cancel')
    try {
      const wasActive = isActive(job)
      await cancelImportJob(job.id)
      setConfirmCancel(false)
      setReviewing(false)
      setNotice(wasActive ? `${t('import.canceled')} ${kept(downloads)}` : t('import.canceled'))
      await load()
    } catch (e) {
      setConfirmCancel(false)
      setProblem(errorText(e))
    } finally {
      setBusy('')
    }
  }
  const retry = async (groupId: number, anyFormat?: boolean) => {
    setProblem('')
    try {
      await retryImportGroup(groupId, anyFormat)
    } catch (e) {
      setProblem(errorText(e))
    }
    await refresh()
  }

  /** The review closes; when its button is gone (all decided), focus goes to the progress line. */
  const closeReview = () => {
    setReviewing(false)
    window.requestAnimationFrame(() => {
      const a = document.activeElement
      if (!a || a === document.body) document.getElementById('import-ready')?.focus()
    })
  }

  let body
  if (!loaded) {
    body = (
      <div className={m.spinnerRow} role="status">
        <span className={m.spinner} aria-hidden /> {t('sources.loading')}
      </div>
    )
  } else if (error && !services) {
    body = (
      <div className={m.problem} role="alert">
        <Icon name="cloud" size={18} /> {error}
        <Button size="small" variant="secondary" onClick={() => void load()}>
          {t('common.retry')}
        </Button>
      </div>
    )
  } else if (!job) {
    body = (
      Services && services && services.length > 0 ? (
        <Suspense
          fallback={
            <div className={m.spinnerRow} role="status">
              <span className={m.spinner} aria-hidden /> {t('sources.loading')}
            </div>
          }
        >
          <Services
            services={services}
            onFiles={(f) => void upload(f)}
            uploading={busy === 'upload'}
            onJob={show}
            onStart={() => {
              setProblem('')
              setNotice('')
            }}
            onProblem={setProblem}
          />
        </Suspense>
      ) : (
        <FileImport onFiles={(f) => void upload(f)} busy={busy === 'upload'} />
      )
    )
  } else if (job.status === 'reading') {
    body = (
      <div className={styles.reading}>
        <div className={m.spinnerRow} role="status">
          <span className={m.spinner} aria-hidden /> {t('import.reading')}
        </div>
        {/* the read (a Takeout's videos being named can take minutes) stops with the job */}
        <Button size="small" variant="secondary" loading={busy === 'cancel'} disabled={busy !== ''} onClick={() => void cancel()}>
          {t('import.cancel')}
        </Button>
      </div>
    )
  } else if (job.status === 'preview') {
    body = (
      <>
        <h3 className={m.groupTitle}>{serviceName(job.service)}</h3>
        <ImportPreview
          job={job}
          busy={busy === 'start' || busy === 'cancel' ? busy : ''}
          onPatch={(p) => void patch(p)}
          onStart={() => void start()}
          onCancel={() => void cancel()}
        />
      </>
    )
  } else {
    body = (
      <>
        <h3 className={m.groupTitle}>{serviceName(job.service)}</h3>
        <ImportProgress
          job={job}
          groups={groups}
          onRetry={downloads ? retry : undefined}
          onReview={() => setReviewing(true)}
          onCancel={() => setConfirmCancel(true)}
          onDone={dismiss}
        />
      </>
    )
  }

  return (
    <section aria-labelledby="import-title" className={styles.page}>
      <Header downloads={downloads} />
      {problem && (
        <div className={`${m.problem} ${styles.error}`} role="alert">
          <Icon name="warning" size={18} /> {problem}
        </div>
      )}
      {notice && !job && (
        <p className={styles.notice} role="status">
          <Icon name="checkCircle" size={18} aria-hidden /> {notice}
        </p>
      )}
      {stale && job && (
        <div className={m.problem} role="alert" data-testid="import-stale">
          <Icon name="cloud" size={18} aria-hidden /> {t('import.stale')}
          <Button size="small" variant="secondary" onClick={() => void refresh()}>
            {t('common.retry')}
          </Button>
        </div>
      )}
      {body}

      {conflict && (
        <Sheet
          title={t('import.signIn.running')}
          message={
            conflict.open
              ? t('import.conflict.open', { service: serviceName(conflict.open.service), songs: conflict.open.counts.songs }) +
                (isActive(conflict.open) ? ` ${t(downloads ? 'import.conflict.active' : 'import.conflict.activeLocal')} ${kept(downloads)}` : '')
              : t('import.conflict.generic')
          }
          busy={busy === 'cancel'}
          onCancel={() => setConflict(null)}
          actions={[
            ...(conflict.open ? [{ label: t('import.conflict.cancelAndUpload'), kind: 'destructive' as const, run: () => void cancelOpenAndUpload() }] : []),
            { label: t('import.conflict.openCurrent'), kind: 'primary', run: continueOpen },
          ]}
        />
      )}
      {confirmCancel && job && (
        <Sheet
          title={t('import.confirm.title')}
          message={t(downloads ? 'import.confirm.message' : 'import.confirm.messageLocal', { kept: kept(downloads) })}
          busy={busy === 'cancel'}
          onCancel={() => setConfirmCancel(false)}
          actions={[
            { label: t('import.confirm.keep'), kind: 'cancel', run: () => setConfirmCancel(false) },
            { label: t('import.cancel'), kind: 'destructive', run: () => void cancel() },
          ]}
        />
      )}
      {reviewing && job && (
        <Sheet
          title={t('import.review.title')}
          message={t('import.review.message')}
          role="dialog"
          size="large"
          onCancel={closeReview}
        >
          <ReviewList jobId={job.id} onChanged={() => void refresh()} />
        </Sheet>
      )}
    </section>
  )
}
