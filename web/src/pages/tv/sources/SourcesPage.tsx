// «Внешние источники» (/tv/manage/sources, admin): the external playlists as
// a compact list — name, masked address, status, counts, new channels; a
// refresh that lost too many channels asks right under its row — each
// opening its own page (settings and channels); then the sets, the devices
// they are assigned to, and the TV settings (Xtream Codes).
import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { APP_NAME } from '../../../brand'
import { errorText, getUsers } from '../../../api/client'
import { confirmDrop, createSource, getDevices, getSets, getSources, putTvSettings, refreshSource, rejectDrop, type ChannelSet, type Device, type Source } from '../../../api/iptv'
import type { User } from '../../../api/types'
import { Icon } from '../../../components/Icon'
import { locale, useT, type TFunction } from '../../../i18n'
import { ago } from '../../../state/format'
import { Badge } from '../../../ui/Badge'
import { Button, ButtonLink } from '../../../ui/Button'
import { GroupedSection, ListRow } from '../../../ui/List'
import { Notice } from '../../../ui/Notice'
import { Toast } from '../../../ui/Toast'
import { Toggle } from '../../../ui/Toggle'
import { DevicesPanel, SetsPanel } from './SetsDevices'
import { SourceEditor } from './SourceEditor'
import styles from './Sources.module.css'

export const num = (n: number) => n.toLocaleString(locale())

/** «Обновлён 3 ч назад» / «Ещё не обновлялся». */
export const refreshedText = (t: TFunction, s: Source) => (s.lastOk ? t('iptv.refreshed', { ago: ago(s.lastOk) }) : t('iptv.refreshedNever'))

/** Where a source comes from, without its secrets. */
export const sourceAddress = (s: Source) => (s.kind === 'xtream' && s.xtream ? `${s.xtream.server} · ${s.xtream.user}` : s.url)

/** The answer of a refresh, in a line. */
export function refreshSummary(t: TFunction, r: { ok: boolean; error?: string; result: { total: number; new: number; lost: number; pendingDrop: number } }) {
  if (!r.ok) return t('iptv.refreshFailed', { error: r.error ?? '' })
  if (r.result.pendingDrop) return t('iptv.drop.title', { lost: num(r.result.pendingDrop), total: num(r.result.total + r.result.pendingDrop) })
  return t('iptv.refreshDone', { total: num(r.result.total), new: num(r.result.new), lost: num(r.result.lost) })
}

/** The pending-drop notice: «Подтвердить» (destructive) takes the shorter list, the other keeps the old one. */
export function DropNotice({ s, busy, onConfirm, onKeep, as }: { s: Source; busy?: string; onConfirm: () => void; onKeep: () => void; as?: 'li' | 'div' }) {
  const t = useT()
  const total = s.counts.total
  return (
    <Notice
      as={as}
      title={t('iptv.drop.title', { lost: num(s.pendingDrop), total: num(total) })}
      actions={
        <>
          <Button variant="secondary" size="small" loading={busy === 'keep'} disabled={!!busy} onClick={onKeep}>
            {t('iptv.drop.keep')}
          </Button>
          <Button variant="destructive" size="small" loading={busy === 'confirm'} disabled={!!busy} onClick={onConfirm}>
            {t('iptv.drop.confirm')}
          </Button>
        </>
      }
    >
      {t('iptv.drop.message')}
    </Notice>
  )
}

export function SourcesPage() {
  const t = useT()
  const navigate = useNavigate()
  const [sources, setSources] = useState<Source[] | null>(null)
  const [xtream, setXtream] = useState(false)
  const [sets, setSets] = useState<ChannelSet[]>([])
  const [devices, setDevices] = useState<Device[]>([])
  const [users, setUsers] = useState<User[]>([])
  const [error, setError] = useState('')
  const [busy, setBusy] = useState<Record<number, string>>({})
  const [adding, setAdding] = useState(false)
  const [toast, setToast] = useState('')

  useEffect(() => {
    document.title = `${t('iptv.title')} — ${APP_NAME}`
  }, [t])
  const load = useCallback(() => {
    getSources().then(
      (r) => {
        setSources(r.sources)
        setXtream(r.xtreamEnabled)
        setError('')
      },
      (e: unknown) => setError(errorText(e, t('iptv.loadFailed'))),
    )
    getSets().then(setSets, () => undefined)
    getDevices().then(setDevices, () => undefined)
    getUsers().then(setUsers, () => undefined)
  }, [t])
  useEffect(load, [load])
  useEffect(() => {
    if (!toast) return
    const id = window.setTimeout(() => setToast(''), 4000)
    return () => window.clearTimeout(id)
  }, [toast])

  const replace = (s: Source) => setSources((list) => list?.map((x) => (x.id === s.id ? s : x)) ?? null)
  const drop = (s: Source, what: 'confirm' | 'keep') => {
    setBusy((b) => ({ ...b, [s.id]: what }))
    const done = () => setBusy(({ [s.id]: _, ...b }) => b)
    const call = what === 'confirm' ? confirmDrop(s.id).then((r) => r.source) : rejectDrop(s.id)
    call.then(
      (next) => {
        replace(next)
        setToast(what === 'confirm' ? t('iptv.drop.confirmed') : t('iptv.drop.kept'))
        done()
      },
      (e: unknown) => {
        setToast(errorText(e, t('common.genericError')))
        done()
      },
    )
  }

  return (
    <div className={styles.page}>
      <div className={styles.column}>
        <ButtonLink to="/tv/manage" variant="plain" size="small" icon="chevronLeft" className={styles.back}>
          {t('iptv.back')}
        </ButtonLink>
        <header className={styles.header}>
          <h1 className={styles.title}>{t('iptv.title')}</h1>
          <Button icon="plus" onClick={() => setAdding(true)}>
            {t('iptv.add')}
          </Button>
        </header>

        {error && (
          <div className={styles.empty} role="alert">
            <p>{error}</p>
            <Button variant="secondary" onClick={load}>
              {t('common.retry')}
            </Button>
          </div>
        )}
        {sources && sources.length === 0 && (
          <div className={styles.empty}>
            <Icon name="broadcast" size={44} strokeWidth={1.4} />
            <h2>{t('iptv.emptyTitle')}</h2>
            <p>{t('iptv.emptyHint')}</p>
          </div>
        )}
        {sources && sources.length > 0 && (
          <GroupedSection title={t('iptv.sources')}>
            {sources.map((s) => (
              <SourceRow key={s.id} s={s} busy={busy[s.id]} onConfirm={() => drop(s, 'confirm')} onKeep={() => drop(s, 'keep')} />
            ))}
          </GroupedSection>
        )}

        {sources && (
          <>
            <SetsPanel sets={sets} sources={sources} devices={devices} onSets={setSets} onToast={setToast} />
            <DevicesPanel sets={sets} devices={devices} users={users} onDevices={setDevices} onReload={() => getDevices().then(setDevices, () => undefined)} onToast={setToast} />
            <GroupedSection title={t('iptv.settings')}>
              <ListRow label={t('iptv.xtream')} hint={t('iptv.xtreamHint')}>
                <Toggle
                  label={t('iptv.xtream')}
                  checked={xtream}
                  onChange={(on) => {
                    setXtream(on)
                    putTvSettings({ xtreamEnabled: on }).catch((e: unknown) => {
                      setXtream(!on)
                      setToast(errorText(e, t('common.genericError')))
                    })
                  }}
                />
              </ListRow>
            </GroupedSection>
          </>
        )}
      </div>

      {adding && (
        <SourceEditor
          source={null}
          xtreamEnabled={xtream}
          users={users}
          onClose={() => setAdding(false)}
          onSave={async (body) => {
            const s = await createSource(body)
            setAdding(false)
            const r = await refreshSource(s.id).catch(() => null)
            if (r) setToast(refreshSummary(t, r))
            navigate(`/tv/manage/sources/${s.id}`)
          }}
        />
      )}
      {toast && <Toast>{toast}</Toast>}
    </div>
  )
}

function SourceRow({ s, busy, onConfirm, onKeep }: { s: Source; busy?: string; onConfirm: () => void; onKeep: () => void }) {
  const t = useT()
  const c = s.counts
  return (
    <>
      <ListRow
        className={styles.linkRow}
        leading={
          <span className={styles.glyph}>
            <Icon name="broadcast" size={18} />
          </span>
        }
        label={
          <span className={styles.nameLine}>
            <Link to={`/tv/manage/sources/${s.id}`} className={styles.rowLink}>
              {s.name}
            </Link>
            {c.new > 0 && (
              <Badge tone="accent" round>
                {t('iptv.newCount', { count: c.new })}
              </Badge>
            )}
          </span>
        }
        hint={
          <>
            <span className={styles.url}>{sourceAddress(s)}</span>
            <span className={styles.line}>
              {[t('iptv.counts', { total: num(c.total), shown: num(c.shown), hidden: num(c.hidden) }), refreshedText(t, s)].join(' · ')}
            </span>
          </>
        }
        status={s.lastError ? <span className={styles.error}>{t('iptv.lastError', { error: s.lastError })}</span> : undefined}
      >
        <Icon name="chevronRight" size={16} className={styles.chevron} />
      </ListRow>
      {s.pendingDrop > 0 && <DropNotice as="li" s={s} busy={busy} onConfirm={onConfirm} onKeep={onKeep} />}
    </>
  )
}
