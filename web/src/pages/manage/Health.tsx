// «Управление → Состояние» (admin): is everything working, on one page —
// the versions (the server's and its latest public release, every addon's,
// whether it runs and whether its web part loaded in this browser, and its newer release with «Обновить»: the same preview
// sheet and update as «Расширения», AddSheet; a check still running or
// failed says so, never «up to date»), the hardware (whether video
// decodes and encodes on the GPU, whether intros can be analysed), the
// folders (whether the server can read and write each), the network
// (whether the server reaches GitHub and its metadata sources, and each
// addon's helper services, /api/addons/helpers) and the
// library (what the last scan found). «Проверить снова» asks everything
// again. The server itself has no update button: it cannot replace its own
// container (the image is pulled from outside, docker compose), so the page
// gives the command. It replaced «Обновления» (/manage/updates leads here).
// Each part reads an endpoint of its own: /api/updates, /api/addons,
// /api/settings/transcoding, /api/settings/storage,
// /api/admin/health/network, /api/library/status.
import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { APP_NAME } from '../../brand'
import { errorText, getAddonHelpers, getAddons, getCoreUpdate, getLibraryStatus, getNetworkHealth, getTranscoding } from '../../api/client'
import { getStorage, type StorageView } from '../../api/storage'
import type { AddonInfo, CoreUpdate, HelperHealth, LibraryStatus, NetCheck, TranscodingView } from '../../api/types'
import { StoragePerms } from '../../components/storage/StoragePerms'
import { t as tr, useT, type Key } from '../../i18n'
import { showNotice } from '../../state/notice'
import { useSession } from '../../state/session'
import { Button, ButtonAnchor } from '../../ui/Button'
import { GroupedSection, ListRow } from '../../ui/List'
import { Notice } from '../../ui/Notice'
import { SettingsIcon } from '../../ui/SettingsIcon'
import { AddSheet, reloadSlots } from './Addons'
import { scanLine } from './Tasks'
import { statusLine } from './TranscodingSettings'
import m from './Manage.module.css'
import styles from './Health.module.css'

/** The command that updates a Compose install (docs/updates.md). */
export const COMPOSE_UPDATE = 'docker compose pull && docker compose up -d'

const CHECKING_POLL_MS = 3000

type Load<T> = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; value: T }

/** Starts `call`, putting its state into `set` (a failure: `failed`'s words). */
function fetchInto<T>(set: (l: Load<T>) => void, call: (signal?: AbortSignal) => Promise<T>, failed: Key, signal?: AbortSignal) {
  set({ status: 'loading' })
  call(signal).then(
    (value) => !signal?.aborted && set({ status: 'ready', value }),
    (e: unknown) => !signal?.aborted && set({ status: 'error', message: errorText(e, tr(failed)) }),
  )
}

export function Health() {
  const t = useT()
  const { user } = useSession()
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
  const withAddons = Boolean(user?.addons)

  const load = useCallback(
    (signal?: AbortSignal, fresh = false) => {
      fetchInto(setCore, getCoreUpdate, 'updates.checkFailed', signal)
      fetchInto(setHw, getTranscoding, 'settings.tc.loadFailed', signal)
      fetchInto(setStorage, getStorage, 'health.foldersFailed', signal)
      fetchInto(setNet, getNetworkHealth, 'health.networkFailed', signal)
      fetchInto(setLibrary, () => getLibraryStatus(), 'health.libraryFailed', signal)
      if (withAddons) {
        fetchInto(setAddons, () => getAddons(fresh), 'updates.addonsFailed', signal)
        fetchInto(setHelpers, getAddonHelpers, 'health.helpersFailed', signal)
        // the loader is its own chunk, already in once addons load
        void import('../../addons/loader').then(
          (l) => !signal?.aborted && setWebFailed(new Map(l.addonWebFailures())),
          () => {},
        )
      }
    },
    // the module's t: the loads do not follow the language (useT's changes each render)
    [withAddons],
  )
  useEffect(() => {
    document.title = `${tr('manage.section.health')} — ${APP_NAME}`
    const ac = new AbortController()
    load(ac.signal)
    return () => ac.abort()
  }, [load])
  // A check the server is still running (GitHub slow) is asked again
  // quietly until it has an answer.
  const checking = addons.status === 'ready' && addons.value.some((a) => a.updateState === 'checking')
  useEffect(() => {
    if (!checking) return
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
  }, [checking, addons])

  const copy = () =>
    navigator.clipboard?.writeText(COMPOSE_UPDATE).then(
      () => showNotice(t('updates.copied')),
      () => {},
    )
  const busy = [core, hw, storage, net].some((l) => l.status === 'loading')

  return (
    <section aria-labelledby="health-title">
      <header className={m.header}>
        <div>
          <h2 id="health-title" className={m.title}>
            {t('manage.section.health')}
          </h2>
          <p className={m.subtitle}>{t('health.subtitle')}</p>
        </div>
        <div className={m.headerActions}>
          <Button variant="secondary" size="small" icon="restart" loading={busy} onClick={() => load(undefined, true)}>
            {t('updates.recheck')}
          </Button>
        </div>
      </header>

      <GroupedSection title={t('health.versions')}>
        <CoreRow core={core} />
        {withAddons && addons.status === 'loading' && <ListRow label={<span className={m.rowHint}>{t('updates.loading')}…</span>} />}
        {withAddons && addons.status === 'error' && (
          <Notice as="li" tone="destructive">
            {addons.message}
          </Notice>
        )}
        {withAddons &&
          addons.status === 'ready' &&
          addons.value.map((a) => (
            <ListRow
              key={a.id}
              leading={<SettingsIcon glyph="puzzle" tint="green" />}
              label={a.name}
              hint={
                <>
                  {a.update && !a.pinned ? (
                    <span className={styles.newer}>{t('addons.versionUpdate', { version: a.version, update: a.update })}</span>
                  ) : (
                    t('addons.version', { version: a.version })
                  )}{' '}
                  · <span className={addonTone(a)}>{t(`addons.status.${a.status}` as Key)}</span>
                </>
              }
              status={
                <>
                  {/* a newer release is already in the version line */}
                  {!a.update && <AddonUpdateStatus a={a} />}
                  {webFailed.has(a.id) && (
                    <>
                      {!a.update && <br />}
                      <span className={styles.problem}>{t('health.webFailed', { error: webFailed.get(a.id) ?? '' })}</span>
                    </>
                  )}
                </>
              }
            >
              {a.update && !a.pinned && (
                <Button size="small" variant="secondary" aria-label={t('addons.updateNamed', { name: a.name })} onClick={() => setUpdating(a)}>
                  {t('addons.update')}
                </Button>
              )}
              {!a.update && !a.pinned && a.updateState === 'failed' && (
                <Button size="small" variant="secondary" aria-label={t('updates.recheckNamed', { name: a.name })} onClick={() => load(undefined, true)}>
                  {t('updates.recheck')}
                </Button>
              )}
            </ListRow>
          ))}
      </GroupedSection>

      {withAddons && helpers.status !== 'loading' && (helpers.status === 'error' || helpers.value.length > 0) && (
        <GroupedSection title={t('health.helpers')} footer={t('health.helpersFooter')}>
          {helpers.status === 'error' ? (
            <ListRow label={t('health.helpers')} status={<Tone ok={false}>{helpers.message}</Tone>} />
          ) : (
            helpers.value.map((h) => {
              const owner = addons.status === 'ready' ? addons.value.find((a) => a.id === h.addon)?.name : undefined
              return (
                <ListRow
                  key={`${h.addon}:${h.name}`}
                  leading={<SettingsIcon glyph="antenna" tint="cyan" />}
                  label={owner ? `${h.name} · ${owner}` : h.name}
                  hint={<span className={styles.mono}>{h.address}</span>}
                  status={<Tone ok={h.ok}>{h.ok ? t('health.helperOk') : t('health.helperDown', { name: h.name, error: h.error ?? '' })}</Tone>}
                />
              )
            })
          )}
        </GroupedSection>
      )}

      {core.status === 'ready' && core.value.newer && (
        <GroupedSection title={t('updates.howTitle')} footer={core.value.docker ? t('updates.howFoot') : undefined}>
          <ListRow wide label={core.value.docker ? t('updates.howDocker') : t('updates.howBinary')}>
            {core.value.docker && (
              <div className={styles.command}>
                <code className={styles.code}>{COMPOSE_UPDATE}</code>
                <Button size="small" variant="secondary" onClick={() => void copy()}>
                  {t('updates.copy')}
                </Button>
              </div>
            )}
          </ListRow>
        </GroupedSection>
      )}

      <GroupedSection title={t('health.hardware')}>
        <Line icon={<SettingsIcon glyph="cpu" tint="gray" />} label={t('health.decoding')} load={hw} text={(v) => statusLine(v, t)} ok={hwOk} />
        {hw.status === 'ready' && hw.value.fingerprint !== undefined && (
          <ListRow
            leading={<SettingsIcon glyph="playRect" tint="red" />}
            label={t('settings.tc.fingerprint')}
            status={<Tone ok={hw.value.fingerprint}>{t(hw.value.fingerprint ? 'settings.tc.fingerprint.ok' : 'settings.tc.fingerprint.missing')}</Tone>}
          />
        )}
      </GroupedSection>

      <GroupedSection title={t('health.network')} footer={t('health.networkFooter')}>
        {net.status !== 'ready' ? (
          <Line icon={<SettingsIcon glyph="globe" tint="blue" />} label={t('health.network')} load={net} text={() => ''} ok={() => true} />
        ) : (
          net.value.map((n) => (
            <ListRow
              key={n.id}
              leading={<SettingsIcon glyph="globe" tint="blue" />}
              label={n.name}
              status={<Tone ok={n.ok}>{n.ok ? t('health.reached', { ms: n.ms }) : t('health.unreached', { error: n.error ?? '' })}</Tone>}
            />
          ))
        )}
      </GroupedSection>

      <GroupedSection title={t('health.library')}>
        <Line icon={<SettingsIcon glyph="stack" tint="indigo" />} label={t('tasks.scan')} load={library} text={(s) => scanLine(s, t)} ok={(s) => !s.error} />
      </GroupedSection>

      {storage.status === 'ready' ? (
        <StoragePerms view={storage.value} title={t('health.folders')} />
      ) : (
        <GroupedSection title={t('health.folders')}>
          <Line icon={<SettingsIcon glyph="folder" tint="teal" />} label={t('health.folders')} load={storage} text={() => ''} ok={() => true} />
        </GroupedSection>
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

/** The GPU works as chosen (the CPU by choice is fine too). */
function hwOk(v: TranscodingView): boolean {
  const s = v.status
  if (s.mode === 'none') return s.requested === 'none'
  const own = s.probes.find((p) => p.mode === s.mode)
  return !own || own.ok
}

/** An addon's state in its colour: running fine, crashed or not fitting a problem, the rest plain. */
function addonTone(a: AddonInfo): string | undefined {
  if (a.status === 'running') return styles.fine
  if (a.status === 'crashed' || a.status === 'incompatible' || a.pinFailed) return styles.problem
  return undefined
}

/** A status in its colour: fine or a problem. */
function Tone({ ok, children }: { ok: boolean; children: ReactNode }) {
  return <span className={ok ? styles.fine : styles.problem}>{children}</span>
}

/** One row of a load: checking, its failure, or its line in its colour. */
function Line<T>({ icon, label, load, text, ok }: { icon: ReactNode; label: string; load: Load<T>; text: (v: T) => string; ok: (v: T) => boolean }) {
  const t = useT()
  if (load.status === 'loading') return <ListRow leading={icon} label={label} hint={`${t('updates.loading')}…`} />
  if (load.status === 'error') return <ListRow leading={icon} label={label} status={<Tone ok={false}>{load.message}</Tone>} />
  return <ListRow leading={icon} label={label} status={<Tone ok={ok(load.value)}>{text(load.value)}</Tone>} />
}

/** An addon's update status: never «up to date» when the check did not say so. */
export function AddonUpdateStatus({ a }: { a: AddonInfo }) {
  const t = useT()
  if (a.pinned) return <>{t('updates.addonPinned')}</>
  if (a.update) return <span className={styles.newer}>{t('updates.available', { version: a.update })}</span>
  switch (a.updateState) {
    case 'checking':
      return <>{t('updates.addonChecking')}</>
    case 'needsCore':
      return <span className={styles.problem}>{a.updateError}</span>
    case 'failed':
      return <span className={styles.problem}>{a.updateError ? t('updates.addonCheckFailed', { reason: a.updateError }) : t('updates.checkFailed')}</span>
  }
  return <>{t('updates.upToDate')}</>
}

/** The server's row: what runs, what is the latest, the release's notes. */
function CoreRow({ core }: { core: Load<CoreUpdate> }) {
  const t = useT()
  const icon = <SettingsIcon glyph="updates" tint="gray" />
  if (core.status === 'loading') return <ListRow leading={icon} label={APP_NAME} hint={`${t('updates.loading')}…`} />
  if (core.status === 'error') return <ListRow leading={icon} label={APP_NAME} status={core.message} />
  const c = core.value
  const latest = c.latest.replace(/^v/, '')
  const versioned = /^\d+\.\d+\.\d+/.test(c.version)
  let status = t('updates.checkFailed')
  if (c.checked) status = c.newer ? t('updates.available', { version: latest }) : versioned ? t('updates.upToDate') : t('updates.devBuild', { version: latest })
  return (
    <ListRow leading={icon} label={APP_NAME} hint={t('updates.installed', { version: c.commit && !versioned ? `${c.version} (${c.commit.slice(0, 7)})` : c.version })} status={c.newer ? <span className={styles.newer}>{status}</span> : status}>
      {c.checked && c.releaseUrl && (
        <ButtonAnchor size="small" variant={c.newer ? 'primary' : 'secondary'} href={c.releaseUrl} target="_blank" rel="noopener noreferrer">
          {t('updates.notes')}
        </ButtonAnchor>
      )}
    </ListRow>
  )
}
