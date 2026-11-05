// «Переезд на Vault» (admin), in both editions: Vault reads what the old
// media servers have (Jellyfin, Kavita, Audiobookshelf) and carries it over.
// Always in this order, each step stated plainly:
//   1. choose what to move (a card per service, named by the server) and
//      whose progress goes to which profile;
//   2. «Проверить» — a dry run: nothing changes anywhere, the list says what
//      would move and what would not;
//   3. «Перенести» — only for the exact choice just checked, after a
//      confirmation; progress comes as migrate.progress.
// The history keeps every report.
//
// Another build adds more services and steps of its own. All of that is ./MigrateFull, loaded lazily and only when
// the server says it has it (`state` in the status); this file never names
// those services. The core build's bundle has no MigrateFull at all.
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useMemo, useRef, useState, type ComponentType } from 'react'
import { ApiError, migrateReport, migrateReports, migrateRun, migrateState, migrateStatus, useEvents } from '../../api/client'
import type {
  MigrateLine,
  MigrateReport,
  MigrateSource,
  MigrateSourceId,
  MigrateState,
  MigrateStatus,
  MigrateStatusKind,
  MigrateUser,
} from '../../api/types'
import { Button } from '../../ui/Button'
import { Icon, type IconName } from '../../components/Icon'
import { Select } from '../../ui/Select'
import { Sheet } from '../../ui/Sheet'
import { locale, t, useT, type Key } from '../../i18n'
import styles from './Migrate.module.css'
import m from './Manage.module.css'

/** The three media servers: the names, icons and texts this file knows. */
const CORE_NAMES: Record<string, string> = {
  jellyfin: 'Jellyfin',
  kavita: 'Kavita',
  audiobookshelf: 'Audiobookshelf',
}

/** What each service brings, under its name, and its icon. */
const CORE_SOURCES: Record<string, SourceMeta> = {
  jellyfin: { icon: 'play', what: 'migrate.what.jellyfin' },
  kavita: { icon: 'book', what: 'migrate.what.kavita' },
  audiobookshelf: { icon: 'headphones', what: 'migrate.what.audiobookshelf' },
}

/** A card's icon, its «what» line and an optional note under it. */
export interface SourceMeta {
  icon: IconName
  what?: Key
  note?: Key
}

/** Busy: what the page is waiting for ('switch' and 'rollback' are the sources build's). */
export type Busy = '' | 'check' | 'run' | 'switch' | 'rollback'

/** What the sources build's parts get from the page. */
export interface MigrateCtx {
  status: MigrateStatus
  running: string | null
  ready: boolean
  busy: Busy
  setBusy: (b: Busy) => void
  setProblem: (text: string) => void
  /** Reads the status again. */
  reload: () => void
  /** A dry run of just these services. */
  check: (only: MigrateSourceId[]) => void
}

/** The sources build's part of the page (./MigrateFull); the core build has none. */
export interface MigrateFullApi {
  /** Icon and texts of a service the core does not know. */
  source(id: string): SourceMeta | undefined
  /** Cards above the services: the switch to the taken-over follows, moving the items again. */
  TopCards: ComponentType<{ ctx: MigrateCtx }>
  /** Next to a history row: giving the items back. */
  HistoryAction: ComponentType<{ ctx: MigrateCtx; report: MigrateReport; rolledBack: boolean; onStarted: (reportId: string) => void }>
  /** The items of the confirmation that belong to the sources build's services. */
  ConfirmItems: ComponentType<{ sources: string[] }>
  /** Said after «Сейчас: …» while this service is moving. */
  progressNote(current: string | undefined): string
  subtitle: Key
}

const loadFull = __LIBRARY_EDITION__ ? null : () => import('./MigrateFull').then((m) => m.migrateFull)
let fullApi: MigrateFullApi | null = null

/** The sources build's part, once it has loaded (always null in the core build). */
function useFull(): MigrateFullApi | null {
  const [api, setApi] = useState(fullApi)
  useEffect(() => {
    if (api || !loadFull) return
    let live = true
    loadFull().then(
      (a) => {
        fullApi = a
        if (live) setApi(a)
      },
      () => {},
    )
    return () => {
      live = false
    }
  }, [api])
  return api
}

const nameOf = (names: Record<string, string>, id: string | undefined) => (id ? (names[id] ?? CORE_NAMES[id] ?? id) : '')

const PROGRESS_SOURCES = new Set(['jellyfin', 'kavita', 'audiobookshelf'])

/** A dry run lets the real run go ahead for an hour (the server says so too). */
const PLAN_TTL_MS = 60 * 60_000
const POLL_MS = 2000

const isZero = (ts: string | undefined) => !ts || ts.startsWith('0001-')
const finished = (r: MigrateReport) => !isZero(r.finishedAt)

const userKey = (u: MigrateUser) => `${u.source}:${u.sourceId}`

/** The choice a plan was made for: sources (sorted) and the profile mapping. */
function planKey(sources: string[], userMap: Record<string, number>) {
  const um = Object.keys(userMap)
    .sort()
    .map((k) => `${k}=${userMap[k]}`)
  return `${[...sources].sort().join(',')}|${um.join(',')}`
}

export function errorText(e: unknown, fallback?: string) {
  return e instanceof ApiError ? e.message : (fallback ?? t('migrate.failedRetry'))
}

/**
 * «Будет перенесено 4 · Пропущено 1 · Не получится 0 · Внимание 2»; a
 * rollback says what it gave back: «Возвращено 3 · Не получилось 1 · Внимание 0».
 */
export function summaryParts(
  r: Pick<MigrateReport, 'counts' | 'dryRun' | 'rollbackOf'>,
): { kind: MigrateStatusKind; label: string; n: number }[] {
  const c = r.counts ?? {}
  const dryRun = r.dryRun
  if (r.rollbackOf) {
    return [
      { kind: 'imported', label: t('migrate.status.returned'), n: c.imported ?? 0 },
      { kind: 'failed', label: t('migrate.status.failed'), n: c.failed ?? 0 },
      { kind: 'warning', label: t('migrate.status.warning'), n: c.warning ?? 0 },
    ]
  }
  return [
    { kind: dryRun ? 'planned' : 'imported', label: dryRun ? t('migrate.status.planned') : t('migrate.status.imported'), n: (dryRun ? c.planned : c.imported) ?? 0 },
    { kind: 'skipped', label: t('migrate.status.skipped'), n: c.skipped ?? 0 },
    { kind: 'failed', label: dryRun ? t('migrate.status.willFail') : t('migrate.status.failed'), n: c.failed ?? 0 },
    { kind: 'warning', label: t('migrate.status.warning'), n: c.warning ?? 0 },
  ]
}

function Summary({ report }: { report: MigrateReport }) {
  useT()
  return (
    <p className={styles.summary} data-testid="migrate-summary">
      {summaryParts(report).map((p, i, all) => (
        // the dot ends an item, so a wrapped line never starts with one
        <span key={p.kind} className={`${styles.sumItem} ${p.n > 0 ? styles[`sum_${p.kind}`] : ''}`}>
          {p.label} {p.n}
          {i < all.length - 1 && <span className={styles.dot} aria-hidden>·</span>}
        </span>
      ))}
    </p>
  )
}

const STATUS_ICON: Record<MigrateStatusKind, IconName> = {
  planned: 'arrowRight',
  imported: 'checkCircle',
  skipped: 'check',
  failed: 'warning',
  warning: 'info',
}
const STATUS_LABEL = {
  planned: 'migrate.status.planned',
  imported: 'migrate.status.imported',
  skipped: 'migrate.status.skipped',
  failed: 'migrate.status.failed',
  warning: 'migrate.status.warning',
} as const satisfies Record<MigrateStatusKind, string>
/** A line's status as said aloud; a rollback's imported line was given back. */
const statusLabel = (r: MigrateReport, l: MigrateLine) => t(r.rollbackOf && l.status === 'imported' ? 'migrate.status.returned' : STATUS_LABEL[l.status])

/** The lines of a report, «Не переносится» first, then by service in run order. */
function Lines({ report, names }: { report: MigrateReport; names: Record<string, string> }) {
  const t = useT()
  const groups = useMemo(() => {
    const out: { key: string; title: string; lines: MigrateLine[] }[] = []
    const by = new Map<string, MigrateLine[]>()
    for (const l of report.lines ?? []) {
      const key = l.kind === 'leftover' ? 'leftover' : l.source || 'vault'
      if (!by.has(key)) {
        by.set(key, [])
        out.push({ key, title: key === 'leftover' ? t('migrate.leftover') : nameOf(names, key), lines: by.get(key)! })
      }
      by.get(key)!.push(l)
    }
    return out
  }, [report, names, t])
  if (groups.length === 0) return <p className={m.groupFoot}>{t('migrate.nothing')}</p>
  return (
    <div className={styles.lineGroups}>
      {groups.map((g) => {
        const problems = g.lines.filter((l) => l.status === 'failed').length
        return (
          <details key={g.key} className={styles.lineGroup} open={g.lines.length <= 12 || problems > 0}>
            <summary>
              <span className={styles.lineGroupTitle}>{g.title}</span>
              <span className={styles.lineGroupCount}>
                {t('migrate.lines', { count: g.lines.length })}
                {problems > 0 && t('migrate.linesFailed', { n: problems })}
              </span>
            </summary>
            <ul className={styles.lines}>
              {g.lines.map((l, i) => (
                <li key={i} className={`${styles.line} ${styles[`st_${l.status}`]}`}>
                  <Icon name={STATUS_ICON[l.status] ?? 'info'} size={16} strokeWidth={2.2} className={styles.lineIcon} aria-label={statusLabel(report, l)} />
                  <span className={styles.lineText}>
                    <span className={styles.lineName}>{l.name}</span>
                    {l.reason && <span className={styles.lineReason}>{l.reason}</span>}
                  </span>
                </li>
              ))}
            </ul>
          </details>
        )
      })}
    </div>
  )
}

function SourceCard({
  s,
  meta,
  checked,
  disabled,
  onToggle,
  problems,
}: {
  s: MigrateSource
  meta: SourceMeta | undefined
  checked: boolean
  disabled: boolean
  onToggle: (on: boolean) => void
  problems: string[]
}) {
  const id = `migrate-src-${s.id}`
  const t = useT()
  return (
    <li className={`${styles.source} ${s.found ? '' : styles.missing} ${checked ? styles.sourceOn : ''}`} data-testid="migrate-source">
      <label htmlFor={id} className={styles.sourceLabel}>
        <input
          id={id}
          type="checkbox"
          className={styles.checkbox}
          checked={checked}
          disabled={disabled || !s.found}
          onChange={(e) => onToggle(e.target.checked)}
        />
        <span className={styles.sourceIcon} aria-hidden>
          <Icon name={meta?.icon ?? 'plug'} size={18} strokeWidth={2} />
        </span>
        <span className={styles.sourceText}>
          <span className={styles.sourceName}>{s.name}</span>
          {meta?.what && <span className={styles.sourceWhat}>{t(meta.what)}</span>}
          <span className={s.found ? styles.sourceFound : styles.sourceDetail}>{s.found ? t('migrate.found', { detail: s.detail }) : s.detail}</span>
          {[s.error, ...problems].filter(Boolean).map((p) => (
            <span key={p} className={styles.sourceError}>
              {p}
            </span>
          ))}
          {meta?.note && s.found && <span className={styles.sourceNote}>{t(meta.note)}</span>}
        </span>
      </label>
    </li>
  )
}

export function Migrate() {
  const [status, setStatus] = useState<MigrateStatus | null>(null)
  const [loadError, setLoadError] = useState('')
  const [selected, setSelected] = useState<Set<MigrateSourceId>>(new Set())
  const [userMap, setUserMap] = useState<Record<string, number>>({})
  const [active, setActive] = useState<MigrateReport | null>(null)
  const [activeId, setActiveId] = useState<string | null>(null)
  const [plan, setPlan] = useState<{ id: string; key: string; at: number } | null>(null)
  const [history, setHistory] = useState<MigrateReport[]>([])
  const [busy, setBusy] = useState<Busy>('')
  const [problem, setProblem] = useState('')
  const [confirmRun, setConfirmRun] = useState(false)
  const inited = useRef(false)
  const reportRef = useRef<HTMLDivElement>(null)
  const t = useT()
  const api = useFull()

  const loadHistory = useCallback(() => {
    migrateReports().then(setHistory, () => {})
  }, [])

  const load = useCallback(() => {
    migrateStatus().then(
      (st) => {
        setStatus(st)
        setLoadError('')
        if (!inited.current) {
          inited.current = true
          setSelected(new Set(st.sources.filter((s) => s.found && !s.error).map((s) => s.id)))
          const um: Record<string, number> = {}
          for (const u of st.users) if (!u.error && u.vaultUserId) um[userKey(u)] = u.vaultUserId
          setUserMap(um)
          if (st.running) setActiveId(st.running)
        }
      },
      (e: unknown) => setLoadError(errorText(e, t('common.loadFailed'))),
    )
  }, [t])

  useEffect(() => {
    document.title = `${t('migrate.docTitle')} — ${APP_NAME}`
    load()
    loadHistory()
  }, [load, loadHistory])
  // what changes during a run or a scan comes from the light state (the full
  // status copies the old databases)
  const mergeState = useCallback((st: MigrateState) => setStatus((s) => (s ? { ...s, ...st } : s)), [])
  // while the library is being scanned, look again now and then
  const waitingScan = status != null && !status.libraryReady.ok
  useEffect(() => {
    if (!waitingScan) return
    const id = window.setInterval(() => migrateState().then(mergeState, () => {}), 3000)
    return () => window.clearInterval(id)
  }, [waitingScan, mergeState])

  // the report being written: poll it (events may be lost) until it ends
  const loadActive = useCallback(
    (id: string) =>
      migrateReport(id).then(
        (r) => {
          setActive(r)
          return r
        },
        () => null,
      ),
    [],
  )
  useEffect(() => {
    if (!activeId) return
    let stop = false
    let timer: number | undefined
    const tick = async () => {
      const r = await loadActive(activeId)
      if (stop) return
      const st = await migrateState().catch(() => null)
      if (stop) return
      if (st) mergeState(st)
      const over = r && (finished(r) || (st != null && st.running !== activeId))
      if (over) {
        setBusy('')
        loadHistory()
        return
      }
      timer = window.setTimeout(tick, POLL_MS)
    }
    void tick()
    return () => {
      stop = true
      window.clearTimeout(timer)
    }
  }, [activeId, loadActive, loadHistory, mergeState])

  useEvents(
    (e) => {
      const d = e.data as { reportId?: string } | null
      if (d?.reportId && d.reportId === activeId) void loadActive(activeId)
    },
    ['migrate.progress'],
  )

  const sources = useMemo(() => [...selected].sort(), [selected])
  const effectiveMap = useMemo(() => {
    // only the profiles of the chosen services count for the plan
    const out: Record<string, number> = {}
    for (const [k, v] of Object.entries(userMap)) if (v > 0 && selected.has(k.split(':')[0] as MigrateSourceId)) out[k] = v
    return out
  }, [userMap, selected])
  const key = planKey(sources, effectiveMap)

  // a finished dry run of exactly this choice becomes the plan
  useEffect(() => {
    if (active && active.dryRun && !active.rollbackOf && finished(active) && active.id === activeId) {
      setPlan((p) => (p?.id === active.id ? p : { id: active.id, key: planKeyOf(active), at: Date.parse(active.finishedAt) }))
    }
  }, [active, activeId])
  // the key the plan was made with is kept client-side at start
  const pending = useRef<Map<string, string>>(new Map())
  function planKeyOf(r: MigrateReport) {
    return pending.current.get(r.id) ?? ''
  }

  const running = status?.running ?? null
  const ready = status?.libraryReady.ok ?? false
  const planValid = plan != null && plan.key === key && Date.now() - plan.at < PLAN_TTL_MS
  const planStale = plan != null && plan.key === key && !planValid
  const canCheck = ready && !running && sources.length > 0 && busy === ''
  const canRun = canCheck && planValid

  const start = async (dryRun: boolean, only?: MigrateSourceId[]) => {
    setProblem('')
    const src = only ?? (sources as MigrateSourceId[])
    const um = only ? {} : effectiveMap
    setBusy(dryRun ? 'check' : 'run')
    try {
      const { reportId } = await migrateRun({ sources: src, dryRun, planId: dryRun ? undefined : plan?.id, userMap: um })
      pending.current.set(reportId, planKey(src, um))
      if (!dryRun) setPlan(null) // the next run needs its own check
      if (only) setSelected(new Set(only))
      setActive(null)
      setActiveId(reportId)
      setStatus((s) => (s ? { ...s, running: reportId } : s))
      window.setTimeout(() => reportRef.current?.scrollIntoView?.({ behavior: 'smooth', block: 'start' }), 50)
    } catch (e) {
      setBusy('')
      setProblem(errorText(e))
    }
  }

  // the sources build's rollback has started a report of its own
  const startedRollback = (reportId: string) => {
    setActive(null)
    setActiveId(reportId)
    setStatus((s) => (s ? { ...s, running: reportId } : s))
  }

  const openReport = (id: string) => {
    setActive(null)
    setActiveId(id)
    window.setTimeout(() => reportRef.current?.scrollIntoView?.({ behavior: 'smooth', block: 'start' }), 50)
  }

  if (!status) {
    return (
      <section aria-labelledby="migrate-title">
        <Header subtitle="migrate.subtitle" />
        {loadError ? (
          <div className={m.problem} role="alert">
            <Icon name="cloud" size={18} /> {loadError}
            <Button size="small" variant="secondary" onClick={load}>
              {t('common.retry')}
            </Button>
          </div>
        ) : (
          <div className={m.spinnerRow} role="status">
            <span className={m.spinner} aria-hidden /> {t('migrate.searching')}
          </div>
        )}
      </section>
    )
  }

  const userProblems = (id: string) => status.users.filter((u) => u.source === id && u.error).map((u) => u.error!)
  const users = status.users.filter((u) => !u.error && selected.has(u.source as MigrateSourceId))
  // a rollback cut short by a restart may be finished (the server says when not)
  const rolledBack = new Set(history.filter(finished).map((r) => r.rollbackOf).filter(Boolean))
  const activeRunning = active != null && !finished(active) && running === active.id
  const interrupted = active != null && !finished(active) && running !== active.id && busy === ''
  // the sources build's part only where the server has the old services it is for
  const ext = api && status.state !== undefined ? api : null
  const names = Object.fromEntries(status.sources.map((s) => [s.id, s.name]))
  const ctx: MigrateCtx = { status, running, ready, busy, setBusy, setProblem, reload: load, check: (only) => void start(true, only) }
  const metaOf = (id: string) => CORE_SOURCES[id] ?? ext?.source(id)

  return (
    <section aria-labelledby="migrate-title" className={styles.page}>
      <Header subtitle={ext?.subtitle ?? 'migrate.subtitle'} />

      {!ready && (
        <div className={m.problem} role="status">
          <span className={m.spinner} aria-hidden /> {status.libraryReady.reason || t('migrate.scanning')}
        </div>
      )}
      {problem && (
        <div className={`${m.problem} ${styles.error}`} role="alert">
          <Icon name="warning" size={18} /> {problem}
        </div>
      )}

      {ext && <ext.TopCards ctx={ctx} />}

      <h3 className={m.groupTitle}>{t('migrate.what')}</h3>
      <ul className={styles.sources} aria-label={t('migrate.oldServices')}>
        {status.sources.map((s) => (
          <SourceCard
            key={s.id}
            s={s}
            meta={metaOf(s.id)}
            checked={selected.has(s.id)}
            disabled={!!running || busy !== ''}
            problems={userProblems(s.id)}
            onToggle={(on) =>
              setSelected((cur) => {
                const n = new Set(cur)
                if (on) n.add(s.id)
                else n.delete(s.id)
                return n
              })
            }
          />
        ))}
      </ul>

      {users.length > 0 && (
        <>
          <h3 className={m.groupTitle}>{t('migrate.profiles')}</h3>
          <ul className={m.group} aria-label={t('migrate.profiles')}>
            {users.map((u) => {
              const k = userKey(u)
              const id = `migrate-user-${k.replace(/[^a-z0-9]/gi, '-')}`
              return (
                <li key={k} className={m.row}>
                  <div className={m.rowText}>
                    <p id={id} className={m.rowLabel}>
                      {u.sourceName}
                    </p>
                    <p className={m.rowHint}>
                      {nameOf(names, u.source)} · {t('migrate.marks', { count: u.rows })}
                    </p>
                  </div>
                  <div className={m.rowControl}>
                    <Select
                      label={u.sourceName}
                      placement="bottom-end"
                      className={styles.select}
                      value={String(userMap[k] ?? 0)}
                      disabled={!!running || busy !== ''}
                      options={[{ value: '0', label: t('migrate.dontMove') }, ...status.profiles.map((p) => ({ value: String(p.id), label: p.name }))]}
                      onChange={(v) => setUserMap((cur) => ({ ...cur, [k]: Number(v) }))}
                    />
                  </div>
                </li>
              )
            })}
          </ul>
          <p className={m.groupFoot}>{t('migrate.profiles.foot', { app: APP_NAME })}</p>
        </>
      )}

      {status.leftovers.length > 0 && (
        <>
          <h3 className={m.groupTitle}>{t('migrate.leftover')}</h3>
          <ul className={m.group} aria-label={t('migrate.leftover')}>
            {status.leftovers.map((l, i) => (
              <li key={i} className={m.row}>
                <Icon name="info" size={18} className={styles.leftoverIcon} />
                <div className={m.rowText}>
                  <p className={m.rowLabel}>{l.reason}</p>
                </div>
              </li>
            ))}
          </ul>
        </>
      )}

      <div className={styles.actions}>
        <Button variant="secondary" icon="search" loading={busy === 'check'} disabled={!canCheck} onClick={() => void start(true)}>
          {t('migrate.check')}
        </Button>
        <Button icon="arrowRight" loading={busy === 'run'} disabled={!canRun} onClick={() => setConfirmRun(true)}>
          {t('migrate.run')}
        </Button>
        <p className={styles.actionsHint} aria-live="polite">
          {sources.length === 0
            ? t('migrate.hint.pick')
            : planValid
              ? t('migrate.hint.ready')
              : planStale
                ? t('migrate.hint.stale')
                : t('migrate.hint.first', { app: APP_NAME })}
        </p>
      </div>

      <div ref={reportRef}>
        {activeId && !active && (
          <div className={m.spinnerRow} role="status">
            <span className={m.spinner} aria-hidden /> {t('migrate.loadingReport')}
          </div>
        )}
        {active && (
          <div className={`${styles.report} glass`} data-testid="migrate-report" aria-live="polite">
            <header className={styles.reportHead}>
              <div>
                <h3 className={styles.reportTitle}>{reportTitle(active)}</h3>
                <p className={styles.reportMeta}>
                  {(active.sources ?? []).map((s) => nameOf(names, s)).join(', ')} · {dateText(active.startedAt)}
                </p>
              </div>
              {activeRunning && <span className={m.spinner} aria-label={t('migrate.running')} />}
            </header>
            {activeRunning && <RunProgress report={active} names={names} note={ext?.progressNote} />}
            {interrupted && (
              <p className={styles.interrupted} role="alert">
                {t('migrate.interrupted', { app: APP_NAME })}
              </p>
            )}
            <Summary report={active} />
            <Lines report={active} names={names} />
          </div>
        )}
      </div>

      {history.length > 0 && (
        <>
          <h3 className={m.groupTitle}>{t('migrate.history')}</h3>
          <ul className={m.group} aria-label={t('migrate.history.aria')}>
            {history.map((r) => (
              <li key={r.id} className={`${m.row} ${styles.historyRow}`}>
                <div className={m.rowText}>
                  <p className={m.rowLabel}>
                    {reportTitle(r)} · {dateText(r.startedAt)}
                  </p>
                  <p className={m.rowHint}>
                    {summaryParts(r)
                      .map((p) => `${p.label} ${p.n}`)
                      .join(' · ')}
                  </p>
                </div>
                <div className={`${m.rowControl} ${styles.historyControl}`}>
                  <Button size="small" variant="plain" onClick={() => openReport(r.id)}>
                    {t('migrate.open')}
                  </Button>
                  {/* the sources build: a run cut short by a restart keeps what it moved, and can give it back */}
                  {ext && <ext.HistoryAction ctx={ctx} report={r} rolledBack={rolledBack.has(r.id)} onStarted={startedRollback} />}
                </div>
              </li>
            ))}
          </ul>
        </>
      )}

      {confirmRun && (
        <Sheet
          title={t('migrate.confirm.title', { app: APP_NAME })}
          wide
          icon={<Icon name="arrowRight" size={34} strokeWidth={1.6} />}
          message={
            <ul className={styles.confirmList}>
              {ext && <ext.ConfirmItems sources={sources} />}
              {sources.some((s) => PROGRESS_SOURCES.has(s)) && <li>{t('migrate.confirm.progress')}</li>}
              <li>{t('migrate.readOnly', { app: APP_NAME })}</li>
            </ul>
          }
          onCancel={() => setConfirmRun(false)}
          actions={[
            { label: t('common.cancel'), kind: 'cancel', run: () => setConfirmRun(false) },
            {
              label: t('migrate.run'),
              kind: 'primary',
              run: () => {
                setConfirmRun(false)
                void start(false)
              },
            },
          ]}
        />
      )}
    </section>
  )
}

function Header({ subtitle }: { subtitle: Key }) {
  const t = useT()
  return (
    <header className={m.header}>
      <div>
        <h2 id="migrate-title" className={m.title}>
          {t('migrate.title', { app: APP_NAME })}
        </h2>
        <p className={`${m.subtitle} ${styles.subtitle}`}>
          {t(subtitle, { app: APP_NAME })} {t('migrate.readOnly', { app: APP_NAME })}
        </p>
      </div>
    </header>
  )
}

function RunProgress({ report, names, note }: { report: MigrateReport; names: Record<string, string>; note?: (current: string | undefined) => string }) {
  const t = useT()
  // sources done = those with a line, minus the one being worked on
  const total = report.sources.length
  const seen = new Set((report.lines ?? []).filter((l) => l.kind !== 'leftover').map((l) => l.source))
  const done = Math.max(0, Math.min(total, [...seen].length - 1))
  const current = report.sources.find((s) => !seen.has(s)) ?? report.sources[Math.max(0, seen.size - 1)]
  const pct = total > 0 ? Math.round(((done + 0.5) / total) * 100) : 0
  return (
    <div className={styles.progress}>
      <div className={styles.progressBar} role="progressbar" aria-valuemin={0} aria-valuemax={total} aria-valuenow={done} aria-label={t('migrate.progress')}>
        <span style={{ width: `${pct}%` }} />
      </div>
      <p className={styles.progressText}>
        {t('migrate.progress.now', { name: nameOf(names, current), done, total })}
        {note?.(current)}
      </p>
    </div>
  )
}

function reportTitle(r: MigrateReport) {
  if (r.rollbackOf) return t('migrate.report.rollback')
  return r.dryRun ? t('migrate.report.check') : t('migrate.report.run')
}

function dateText(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString(locale(), { day: 'numeric', month: 'long', hour: '2-digit', minute: '2-digit' })
}
