// The state of «Импорт»: the service tiles and the job the page shows, kept
// fresh by import.progress events with a 10 s poll as the fallback (events
// can be lost). The job shown is the running one, else the newest still in
// reading or preview, else the newest finished one not yet put away with
// «Готово». After a sign-in the page asks for the job it started (?job=).
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, getImportGroups, getImportJob, getImportJobs, getImportServices, useEvents } from '../../../api/client'
import type { ImportGroup, ImportJob, ImportProgressEvent, ImportService } from '../../../api/types'
import { t } from '../../../i18n'

export const POLL_MS = 10_000
/** Events come every 2 s per job at most; one reload of the albums per burst. */
const REFRESH_DEBOUNCE_MS = 800
const DISMISSED_KEY = 'vault.import.dismissed'

export const isActive = (j: ImportJob) => j.status === 'matching' || j.status === 'downloading'
export const isOpen = (j: ImportJob) => j.status === 'reading' || j.status === 'preview' || isActive(j)
const hasGroups = (j: ImportJob) => isActive(j) || j.status === 'done'

function readDismissed(): number {
  try {
    return Number(localStorage.getItem(DISMISSED_KEY)) || 0
  } catch {
    return 0
  }
}

/** The job the page shows (finished jobs up to `dismissed` are put away). */
export function pickJob(jobs: ImportJob[], dismissed: number): ImportJob | null {
  const byNewest = [...jobs].sort((a, b) => b.id - a.id)
  return (
    byNewest.find(isActive) ??
    byNewest.find(isOpen) ??
    byNewest.find((j) => (j.status === 'done' || j.status === 'failed') && j.id > dismissed) ??
    null
  )
}

const message = (e: unknown, fallback: string) => (e instanceof ApiError ? e.message : fallback)

/** `prefer`: a job to show on the first load (?job= after a sign-in), if it is the profile's. */
export function useImportJob(prefer?: number) {
  const [services, setServices] = useState<ImportService[] | null>(null)
  const [job, setJobState] = useState<ImportJob | null>(null)
  const [groups, setGroups] = useState<ImportGroup[]>([])
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState('')
  /** The last reload failed: what is shown may be out of date (kept until the next success). */
  const [stale, setStale] = useState(false)
  const jobRef = useRef<ImportJob | null>(null)
  jobRef.current = job
  const preferRef = useRef(prefer)

  const setJob = useCallback((j: ImportJob | null) => {
    jobRef.current = j
    setJobState(j)
    if (!j) {
      setGroups([])
      setStale(false)
    }
  }, [])

  const loadGroups = useCallback((j: ImportJob) => {
    if (!hasGroups(j)) return
    getImportGroups(j.id).then(
      (gs) => {
        if (jobRef.current?.id === j.id) setGroups(gs)
      },
      () => {
        if (jobRef.current?.id === j.id) setStale(true)
      },
    )
  }, [])

  /** Services and jobs from scratch (first load, «Продолжить», after a cancel). */
  const load = useCallback(async () => {
    try {
      const [ss, jobs] = await Promise.all([getImportServices(), getImportJobs()])
      setServices(ss)
      setStale(false)
      const wanted = preferRef.current
      preferRef.current = undefined // the first load only
      const j = jobs.find((x) => wanted != null && x.id === wanted) ?? pickJob(jobs, readDismissed())
      setJob(j)
      if (j) loadGroups(j)
      setError('')
    } catch (e) {
      setError(message(e, t('import.loadFailed')))
      if (jobRef.current) setStale(true)
    } finally {
      setLoaded(true)
    }
  }, [loadGroups, setJob])

  /** The shown job and its albums again. */
  const refresh = useCallback(async () => {
    const cur = jobRef.current
    if (!cur) return
    try {
      const j = await getImportJob(cur.id)
      if (jobRef.current?.id !== cur.id) return
      setStale(false)
      setJob(j)
      loadGroups(j)
    } catch (e) {
      // deleted elsewhere: look again for what to show
      if (e instanceof ApiError && e.status === 404) void load()
      // no answer: keep what is shown, but say it may be old
      else if (jobRef.current?.id === cur.id) setStale(true)
    }
  }, [load, loadGroups, setJob])

  /** Shows a job the page just got (an upload, a start). */
  const show = useCallback(
    (j: ImportJob) => {
      setJob(j)
      loadGroups(j)
    },
    [loadGroups, setJob],
  )

  /** «Готово»: puts a finished job away (in this browser) and shows the tiles. */
  const dismiss = useCallback(() => {
    const cur = jobRef.current
    if (cur) {
      try {
        localStorage.setItem(DISMISSED_KEY, String(Math.max(cur.id, readDismissed())))
      } catch {
        /* the job comes back on the next visit */
      }
    }
    setJob(null)
  }, [setJob])

  useEffect(() => {
    void load()
  }, [load])

  const timer = useRef<number | undefined>(undefined)
  useEffect(() => () => window.clearTimeout(timer.current), [])
  useEvents(
    (e) => {
      const d = e.data as ImportProgressEvent | null
      if (!d || typeof d.jobId !== 'number') return
      const cur = jobRef.current
      if (!cur) {
        // a job started elsewhere (the app, another tab)
        if (d.status !== 'cancelled') void load()
        return
      }
      if (d.jobId !== cur.id) return
      // the counts are in the event: shown at once; the rest follows
      setJob({ ...cur, status: d.status ?? cur.status, progress: d.progress ?? cur.progress })
      window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => void refresh(), REFRESH_DEBOUNCE_MS)
    },
    ['import.progress'],
  )

  const polling = job != null && (job.status === 'reading' || isActive(job))
  useEffect(() => {
    if (!polling) return
    const id = window.setInterval(() => void refresh(), POLL_MS)
    return () => window.clearInterval(id)
  }, [polling, refresh])

  return { services, job, groups, loaded, error, stale, load, refresh, show, dismiss }
}
