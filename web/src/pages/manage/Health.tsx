// «Управление → Состояние» (admin): is everything working, as one list in
// the System Settings manner. Each check is a row — the ring of its state
// (green check, orange or red «!», a grey dash), its name and a short value;
// the groups are Доступ, Диск и папки, Видео, Каталог и интернет, ТВ и почта,
// the versions (the server's and each addon's) and the addons' helper
// services. A row with something to do is a chevron row: a tap opens the
// sheet «Как починить» — what is wrong, how to fix it, and the one action or
// the one command to copy. Nothing is explained where the state is obvious.
// «Проверить снова» asks everything again; the key of TMDB and the mailbox are
// tested only by the button in their sheets (a login that fails often is
// throttled by the provider).
// Data: /api/admin/selfcheck (what only the server knows: the connection, the
// public address, the settings folder, space, ffmpeg, TMDB, the clock, the
// TV sources, the mail) and the endpoints the page always asked: /api/updates,
// /api/addons, /api/addons/helpers, /api/settings/transcoding,
// /api/settings/storage, /api/admin/health/network, /api/library/status.
// healthChecks.ts turns them into rows. The server cannot update itself (its
// image is pulled from outside), so its sheet gives the command.
import { useCallback, useEffect, useMemo, useState, type Dispatch, type SetStateAction } from 'react'
import { useNavigate } from 'react-router'
import { APP_NAME } from '../../brand'
import { errorText, getAddonHelpers, getAddons, getCoreUpdate, getLibraryStatus, getNetworkHealth, getTranscoding } from '../../api/client'
import { EXTERNAL_PATH, getExternal, saveExternal } from '../../api/external'
import { getSelfCheck, runSelfCheckTest, type SelfCheckView, type TestableCheck } from '../../api/selfcheck'
import { getStorage, type StorageView } from '../../api/storage'
import type { AddonInfo, CoreUpdate, HelperHealth, LibraryStatus, NetCheck, TranscodingView } from '../../api/types'
import { CopyBlock } from '../../components/storage/CopyBlock'
import { locale, t as tr, useT, type Key } from '../../i18n'
import { showNotice } from '../../state/notice'
import { useSession } from '../../state/session'
import { Button, ButtonLink } from '../../ui/Button'
import { GroupedSection, ListLinkRow, ListRow } from '../../ui/List'
import { Notice } from '../../ui/Notice'
import { Sheet, type SheetAction } from '../../ui/Sheet'
import { Spinner } from '../../ui/Spinner'
import { StatusRing, type RingState } from '../../ui/StatusRing'
import { AddSheet, reloadSlots } from './Addons'
import { buildRows, GROUPS, tally, tappable, type CheckRow, type Load, type RowState } from './healthChecks'
import m from './Manage.module.css'
import styles from './Health.module.css'

const CHECKING_POLL_MS = 3000
const RING = 28

/**
 * Starts `call`, putting its state into `set` (a failure: `failed`'s words).
 * An answer already on the page stays while it is asked again, so the rows do
 * not jump; the promise ends when the new one is in.
 */
function fetchInto<T>(set: Dispatch<SetStateAction<Load<T>>>, call: (signal?: AbortSignal) => Promise<T>, failed: Key, signal?: AbortSignal): Promise<void> {
  set((l) => (l.status === 'ready' ? l : { status: 'loading' }))
  return call(signal).then(
    (value) => void (!signal?.aborted && set({ status: 'ready', value })),
    (e: unknown) => void (!signal?.aborted && set({ status: 'error', message: errorText(e, tr(failed)) })),
  )
}

const RINGS: Record<Exclude<RowState, 'checking'>, [RingState, number]> = {
  ok: ['done', 1],
  warn: ['error', 1],
  bad: ['bad', 1],
  off: ['off', 0],
  unknown: ['off', 0],
}

/** A row's state: its ring (named for assistive tech), or a spinner while it is being asked. */
function StateIcon({ state, size = RING }: { state: RowState; size?: number }) {
  const t = useT()
  if (state === 'checking') return <Spinner size="sm" label={t('sc.checking')} />
  const [ring, value] = RINGS[state]
  return (
    <span role="img" aria-label={t(`sc.state.${state}` as Key)} className={styles.lead}>
      <StatusRing state={ring} value={value} size={size} />
    </span>
  )
}

export function Health() {
  const t = useT()
  const navigate = useNavigate()
  const { user } = useSession()
  const [server, setServer] = useState<Load<SelfCheckView>>({ status: 'loading' })
  const [core, setCore] = useState<Load<CoreUpdate>>({ status: 'loading' })
  const [addons, setAddons] = useState<Load<AddonInfo[]>>({ status: 'loading' })
  const [hw, setHw] = useState<Load<TranscodingView>>({ status: 'loading' })
  const [storage, setStorage] = useState<Load<StorageView>>({ status: 'loading' })
  const [net, setNet] = useState<Load<NetCheck[]>>({ status: 'loading' })
  const [library, setLibrary] = useState<Load<LibraryStatus>>({ status: 'loading' })
  const [helpers, setHelpers] = useState<Load<HelperHealth[]>>({ status: 'loading' })
  // the addons whose web part did not load in this browser (addons/loader)
  const [webFailed, setWebFailed] = useState<ReadonlyMap<string, string>>(new Map())
  const [updating, setUpdating] = useState<AddonInfo | null>(null)
  // the row whose sheet is open, and the test (TMDB, the mailbox) that is running
  const [open, setOpen] = useState<string | null>(null)
  const [testing, setTesting] = useState<TestableCheck | null>(null)
  const withAddons = Boolean(user?.addons)

  // everything is being asked (the first time, or «Проверить снова»)
  const [asking, setAsking] = useState(true)
  const load = useCallback(
    (signal?: AbortSignal, fresh = false) => {
      setAsking(true)
      const asked = [
        fetchInto(setServer, (s) => getSelfCheck(fresh, s), 'sc.failed', signal),
        fetchInto(setCore, getCoreUpdate, 'updates.checkFailed', signal),
        fetchInto(setHw, getTranscoding, 'settings.tc.loadFailed', signal),
        fetchInto(setStorage, getStorage, 'health.foldersFailed', signal),
        fetchInto(setNet, getNetworkHealth, 'health.networkFailed', signal),
        fetchInto(setLibrary, () => getLibraryStatus(), 'health.libraryFailed', signal),
      ]
      if (withAddons) {
        asked.push(fetchInto(setAddons, () => getAddons(fresh), 'updates.addonsFailed', signal), fetchInto(setHelpers, getAddonHelpers, 'health.helpersFailed', signal))
        // the loader is its own chunk, already in once addons load
        void import('../../addons/loader').then(
          (l) => !signal?.aborted && setWebFailed(new Map(l.addonWebFailures())),
          () => {},
        )
      }
      void Promise.all(asked).then(() => !signal?.aborted && setAsking(false))
    },
    // the module's t: the loads do not follow the language (useT's changes each render)
    [withAddons],
  )
  // «Внешние сервисы» never chosen: a one-time notice that they can be
  // (closed for good by either of its buttons)
  const [extNotice, setExtNotice] = useState(false)
  useEffect(() => {
    const ac = new AbortController()
    getExternal(ac.signal).then(
      (v) => setExtNotice(Boolean(v.notice)),
      () => {},
    )
    return () => ac.abort()
  }, [])
  const closeExtNotice = () => {
    setExtNotice(false)
    void saveExternal({ dismissNotice: true }).catch(() => {})
  }
  useEffect(() => {
    document.title = `${tr('manage.section.health')} — ${APP_NAME}`
    const ac = new AbortController()
    load(ac.signal)
    return () => ac.abort()
  }, [load])
  // A check the server is still running (GitHub slow) is asked again
  // quietly until it has an answer.
  const addonsChecking = addons.status === 'ready' && addons.value.some((a) => a.updateState === 'checking')
  useEffect(() => {
    if (!addonsChecking) return
    let gone = false
    const timer = window.setTimeout(() => {
      getAddons().then(
        (value) => !gone && setAddons({ status: 'ready', value }),
        () => {},
      )
    }, CHECKING_POLL_MS)
    return () => {
      gone = true
      window.clearTimeout(timer)
    }
  }, [addonsChecking, addons])

  const zone = useMemo(() => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC', [])
  const rows = useMemo(
    () => buildRows({ server, core, addons: withAddons ? addons : null, helpers: withAddons ? helpers : null, hw, storage, net, library, webFailed, zone }, t),
    [server, core, addons, helpers, hw, storage, net, library, webFailed, zone, withAddons, t],
  )
  const { bad, warn, checking } = tally(rows)
  const summary = [bad > 0 && t('sc.summary.bad', { count: bad }), warn > 0 && t('sc.summary.warn', { count: warn })].filter(Boolean).join(' · ')
  const shown = open ? rows.find((r) => r.id === open) : undefined

  /** Asks the server to test TMDB's key or the mailbox, and puts its answer in the list. */
  const test = (id: TestableCheck) => {
    setTesting(id)
    runSelfCheckTest(id)
      .then(
        (c) =>
          setServer((s) => (s.status === 'ready' ? { status: 'ready', value: { ...s.value, checks: s.value.checks.map((x) => (x.id === c.id ? c : x)) } } : s)),
        (e: unknown) => showNotice(errorText(e, tr('sc.failed'))),
      )
      .finally(() => setTesting(null))
  }

  const sheetActions = (row: CheckRow): SheetAction[] => {
    const a = row.action
    const close = () => setOpen(null)
    if (!a) return [{ label: t('common.done'), kind: 'primary', run: close }]
    switch (a.kind) {
      case 'link':
        return [{ label: t(a.label), kind: 'primary', run: () => navigate(a.to) }]
      case 'test':
        return [{ label: t(a.label), kind: 'primary', run: () => test(a.id) }]
      case 'update':
        return [
          {
            label: t(a.label),
            kind: 'primary',
            run: () => {
              close()
              setUpdating(a.addon)
            },
          },
        ]
      case 'recheck':
        return [
          {
            label: t(a.label),
            kind: 'primary',
            run: () => {
              close()
              load(undefined, true)
            },
          },
        ]
    }
  }

  const stamp = server.status === 'ready' ? new Date(server.value.checkedAt).toLocaleTimeString(locale(), { hour: '2-digit', minute: '2-digit' }) : ''

  return (
    <section aria-labelledby="health-title">
      <header className={m.header}>
        <div>
          <h2 id="health-title" className={m.title}>
            {t('manage.section.health')}
          </h2>
          <p className={m.subtitle}>{summary || (asking || checking > 0 ? t('sc.summary.checking') : t('sc.summary.ok'))}</p>
        </div>
        <div className={m.headerActions}>
          <Button variant="secondary" size="small" icon="restart" loading={asking} onClick={() => load(undefined, true)}>
            {t('updates.recheck')}
          </Button>
        </div>
      </header>

      {extNotice && (
        <Notice
          tone="accent"
          title={t('ext.notice.title')}
          actions={
            <>
              <ButtonLink size="small" to={EXTERNAL_PATH} onClick={closeExtNotice}>
                {t('ext.notice.open')}
              </ButtonLink>
              <Button size="small" variant="secondary" onClick={closeExtNotice}>
                {t('ext.notice.dismiss')}
              </Button>
            </>
          }
        >
          {t('ext.notice.body')}
        </Notice>
      )}

      {GROUPS.map((g) => {
        const list = rows.filter((r) => r.group === g.id)
        if (list.length === 0) return null
        return (
          <GroupedSection key={g.id} title={t(g.title)}>
            {list.map((r) => (
              <CheckRowView key={r.id} row={r} onOpen={() => setOpen(r.id)} />
            ))}
          </GroupedSection>
        )
      })}
      {stamp && <p className={styles.stamp}>{t('sc.checkedAt', { time: stamp })}</p>}

      {shown && tappable(shown) && (
        <Sheet
          title={shown.title}
          size="wide"
          role="dialog"
          icon={<StateIcon state={shown.state} size={44} />}
          message={shown.detail}
          busy={testing !== null && shown.action?.kind === 'test' && shown.action.id === testing}
          onCancel={() => setOpen(null)}
          actions={sheetActions(shown)}
        >
          {(shown.fix || shown.command) && (
            <div className={styles.fix}>
              <h3 className={styles.fixTitle}>{t('sc.fixTitle')}</h3>
              {shown.fix && <p className={styles.fixText}>{shown.fix}</p>}
              {shown.command && <CopyBlock text={shown.command} stacked />}
            </div>
          )}
        </Sheet>
      )}

      {updating && (
        <AddSheet
          updating={updating}
          onClose={() => setUpdating(null)}
          onInstalled={(a) => {
            setUpdating(null)
            setAddons((s) => (s.status === 'ready' ? { ...s, value: s.value.map((x) => (x.id === a.id ? a : x)) } : s))
            reloadSlots()
          }}
        />
      )}
    </section>
  )
}

/** One check: a chevron row when it has something to open, else a plain row with its value. */
function CheckRowView({ row, onOpen }: { row: CheckRow; onOpen: () => void }) {
  const tone = row.state === 'warn' ? styles.warn : row.state === 'bad' ? styles.bad : ''
  const value = <span className={[styles.value, tone].filter(Boolean).join(' ')}>{row.value}</span>
  const lead = <StateIcon state={row.state} />
  if (tappable(row)) return <ListLinkRow leading={lead} label={row.title} value={value} onOpen={onOpen} />
  return (
    <ListRow shrink leading={lead} label={row.title}>
      {value}
    </ListRow>
  )
}
