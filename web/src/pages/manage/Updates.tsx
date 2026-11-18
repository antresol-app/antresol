// «Управление → Обновления» (admin): the server's version and its latest
// public release, then every addon's version and its newer release with
// «Обновить» (the same preview sheet and update as «Расширения»: AddSheet;
// the server's `update` field is its check). The server itself has no
// button: it cannot replace its own container (the image is pulled from
// outside, docker compose), so the page says so and gives the command.
import { useCallback, useEffect, useState } from 'react'
import { APP_NAME } from '../../brand'
import { errorText, getAddons, getCoreUpdate } from '../../api/client'
import type { AddonInfo, CoreUpdate } from '../../api/types'
import { t as tr, useT } from '../../i18n'
import { showNotice } from '../../state/notice'
import { useSession } from '../../state/session'
import { Button, ButtonAnchor } from '../../ui/Button'
import { GroupedSection, ListRow } from '../../ui/List'
import { Notice } from '../../ui/Notice'
import { SettingsIcon } from '../../ui/SettingsIcon'
import { AddSheet, reloadSlots } from './Addons'
import m from './Manage.module.css'
import styles from './Updates.module.css'

/** The command that updates a Compose install (docs/install.md). */
export const COMPOSE_UPDATE = 'docker compose pull && docker compose up -d'

type Load<T> = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; value: T }

export function Updates() {
  const t = useT()
  const { user } = useSession()
  const [core, setCore] = useState<Load<CoreUpdate>>({ status: 'loading' })
  const [addons, setAddons] = useState<Load<AddonInfo[]>>({ status: 'loading' })
  const [updating, setUpdating] = useState<AddonInfo | null>(null)
  const withAddons = Boolean(user?.addons)

  const load = useCallback(
    (signal?: AbortSignal) => {
      setCore({ status: 'loading' })
      getCoreUpdate(signal).then(
        (value) => setCore({ status: 'ready', value }),
        (e: unknown) => !signal?.aborted && setCore({ status: 'error', message: errorText(e, tr('updates.checkFailed')) }),
      )
      if (!withAddons) return
      setAddons({ status: 'loading' })
      getAddons().then(
        (value) => !signal?.aborted && setAddons({ status: 'ready', value }),
        (e: unknown) => !signal?.aborted && setAddons({ status: 'error', message: errorText(e, tr('updates.addonsFailed')) }),
      )
    },
    // the module's t: the loads do not follow the language (useT's changes each render)
    [withAddons],
  )
  useEffect(() => {
    document.title = `${tr('manage.section.updates')} — ${APP_NAME}`
    const ac = new AbortController()
    load(ac.signal)
    return () => ac.abort()
  }, [load])

  const copy = () =>
    navigator.clipboard?.writeText(COMPOSE_UPDATE).then(
      () => showNotice(t('updates.copied')),
      () => {},
    )

  return (
    <section aria-labelledby="updates-title">
      <header className={m.header}>
        <div>
          <h2 id="updates-title" className={m.title}>
            {t('manage.section.updates')}
          </h2>
          <p className={m.subtitle}>{t('updates.subtitle')}</p>
        </div>
        <div className={m.headerActions}>
          <Button variant="secondary" size="small" icon="restart" loading={core.status === 'loading'} onClick={() => load()}>
            {t('updates.recheck')}
          </Button>
        </div>
      </header>

      <GroupedSection>
        <CoreRow core={core} />
      </GroupedSection>

      {core.status === 'ready' && (
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

      {withAddons && (
        <GroupedSection title={t('updates.addons')}>
          {addons.status === 'loading' && <ListRow label={<span className={m.rowHint}>{t('updates.loading')}…</span>} />}
          {addons.status === 'error' && (
            <Notice as="li" tone="destructive">
              {addons.message}
            </Notice>
          )}
          {addons.status === 'ready' && addons.value.length === 0 && <ListRow label={<span className={m.rowHint}>{t('updates.addonsEmpty')}</span>} />}
          {addons.status === 'ready' &&
            addons.value.map((a) => (
              <ListRow
                key={a.id}
                leading={<SettingsIcon glyph="puzzle" tint="green" />}
                label={a.name}
                hint={t('updates.installed', { version: a.version })}
                status={
                  a.pinned ? t('updates.addonPinned') : a.update ? <span className={styles.newer}>{t('updates.available', { version: a.update })}</span> : t('updates.upToDate')
                }
              >
                {a.update && !a.pinned && (
                  <Button size="small" variant="secondary" aria-label={t('addons.updateNamed', { name: a.name })} onClick={() => setUpdating(a)}>
                    {t('addons.update')}
                  </Button>
                )}
              </ListRow>
            ))}
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
