// «Управление каналами» (/tv/manage): the channels the profile may edit — an
// admin's shared ones and its own personal ones, a profile's own — as the
// one numbered list, with its theme and what each plays, on/off, «Изменить», «Удалить» (a generated channel
// is turned off instead), «Новый канал» and, for admins, «Обновить
// автоматические каналы».
import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router'
import { APP_NAME } from '../../brand'
import { errorText } from '../../api/client'
import { deleteChannel, generateChannels, getChannels, updateChannel, type Channel, type ChannelRules } from '../../api/channels'
import { Icon } from '../../components/Icon'
import { t as tr, useT } from '../../i18n'
import { useSession } from '../../state/session'
import { useLoad } from '../../state/useLoad'
import { Badge } from '../../ui/Badge'
import { Button } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { GroupedSection, ListRow } from '../../ui/List'
import { Menu, MenuItem, MenuSeparator } from '../../ui/Menu'
import { Sheet } from '../../ui/Sheet'
import { Toast } from '../../ui/Toast'
import { Toggle } from '../../ui/Toggle'
import { ChannelEditor } from './ChannelEditor'
import { ChannelLogo } from './ChannelLogo'
import { byNumber } from './model'
import styles from './Tv.module.css'

/** What a channel plays, in one line («Фильмы · Комедии · 1990-е · Перемешать»). */
export function rulesSummary(r: ChannelRules | undefined, genreName: (key: string) => string = (k) => k): string {
  if (!r) return ''
  const parts: string[] = []
  if (r.picks?.length) parts.push(tr('tv.rules.picksCount', { count: r.picks.length }))
  else if (r.titles?.length) parts.push(tr('tv.rules.titlesCount', { count: r.titles.length }))
  else {
    const k = r.kinds ?? []
    parts.push(k.length === 1 ? (k[0] === 'movie' ? tr('tv.rules.movies') : tr('tv.rules.series')) : tr('tv.rules.moviesAndSeries'))
    if (r.genres?.length) parts.push(r.genres.map(genreName).join(', '))
    if (r.yearFrom) parts.push(tr('tv.rules.decade', { decade: r.yearFrom }))
  }
  if (r.maxRating) parts.push(tr('tv.rules.upTo', { rating: r.maxRating }))
  parts.push(r.order === 'ordered' ? tr('tv.rules.ordered') : tr('tv.rules.shuffle'))
  return parts.join(' · ')
}

const capital = (s: string) => s.charAt(0).toLocaleUpperCase() + s.slice(1)

export function TvManage() {
  const t = useT()
  const { user } = useSession()
  const [params, setParams] = useSearchParams()
  const [edit, setEdit] = useState<{ channel: Channel | null } | null>(params.get('new') ? { channel: null } : null)
  const [removing, setRemoving] = useState<Channel | null>(null)
  const [toast, setToast] = useState('')
  const [busy, setBusy] = useState(false)
  const { state, load, update } = useLoad((signal) => getChannels(signal, true), [], {
    message: (e) => errorText(e, t('common.loadFailed')),
    events: [],
  })
  useEffect(() => {
    document.title = `${t('tv.manageTitle')} — ${APP_NAME}`
  }, [t])
  useEffect(() => {
    if (!toast) return
    const id = window.setTimeout(() => setToast(''), 4000)
    return () => window.clearTimeout(id)
  }, [toast])

  const me = { id: user?.id ?? 0, isAdmin: !!user?.isAdmin }
  const data = state.status === 'ready' ? state.data : null

  const toggle = (c: Channel, on: boolean) => {
    update((d) => ({ ...d, channels: d.channels.map((x) => (x.id === c.id ? { ...x, enabled: on } : x)) }))
    updateChannel(c.id, { name: c.name, number: c.number, category: c.category, personal: c.personal, enabled: on, rules: c.rules ?? {} }).then(
      () => load(true),
      (e: unknown) => {
        setToast(errorText(e, t('common.genericError')))
        load(true)
      },
    )
  }
  const generate = () => {
    setBusy(true)
    generateChannels()
      .then((r) => {
        setToast(t('tv.generated', r))
        load(true)
      })
      .catch((e: unknown) => setToast(errorText(e, t('common.genericError'))))
      .finally(() => setBusy(false))
  }

  return (
    <div className={styles.page}>
      <div className={styles.column}>
        <header className={styles.columnHeader}>
          <h1 className={styles.title}>{t('tv.manageTitle')}</h1>
          <div className={styles.actions}>
            <Button icon="plus" onClick={() => setEdit({ channel: null })}>
              {t('tv.newChannel')}
            </Button>
            {me.isAdmin && (
              <Menu label={t('tv.more')} placement="bottom-end" trigger={(p) => <IconButton {...p} icon="ellipsis" label={t('tv.more')} loading={busy} />}>
                {(close) => <MenuItem icon={<Icon name="restart" size={16} />} label={t('tv.generate')} onSelect={() => (close(false), generate())} />}
              </Menu>
            )}
          </div>
        </header>

        {state.status === 'error' && (
          <div className={styles.empty} role="alert">
            <p>{state.message}</p>
            <Button variant="secondary" onClick={() => load()}>
              {t('common.retry')}
            </Button>
          </div>
        )}
        {data && data.channels.length === 0 && (
          <div className={styles.empty}>
            <Icon name="broadcast" size={48} strokeWidth={1.4} />
            <h2>{t('tv.empty')}</h2>
            <p>{me.isAdmin ? t('tv.emptyAdmin') : t('tv.emptyOthers')}</p>
          </div>
        )}
        {data && data.channels.length > 0 && (
            <GroupedSection title={t('tv.channels')}>
              {byNumber(data.channels).map((c) => (
                <ListRow
                  key={c.id}
                  leading={
                    <span className={styles.rowLead}>
                      <span className={styles.rowNumber}>{c.number}</span>
                      {data.channels.some((x) => x.logoUrl) && <ChannelLogo url={c.logoUrl} size="sm" reserve />}
                    </span>
                  }
                  label={
                    <span className={styles.rowName}>
                      <span>{c.name}</span>
                      {c.personal && me.isAdmin && <Badge>{t('tv.personal')}</Badge>}
                      {c.auto && <Badge>{t('tv.auto')}</Badge>}
                    </span>
                  }
                  hint={[c.category, capital(rulesSummary(c.rules, capital))].filter(Boolean).join(' · ')}
                >
                  <span className={styles.rowActions}>
                    <Toggle label={t('tv.enabled')} checked={c.enabled} onChange={(on) => toggle(c, on)} />
                    <Menu label={c.name} placement="bottom-end" trigger={(p) => <IconButton {...p} size="sm" icon="ellipsis" label={c.name} />}>
                      {(close) => (
                        <>
                          <MenuItem icon={<Icon name="pencil" size={16} />} label={t('tv.edit')} onSelect={() => (close(false), setEdit({ channel: c }))} />
                          <MenuSeparator />
                          <MenuItem
                            destructive
                            icon={<Icon name="trash" size={16} />}
                            label={c.auto ? t('tv.disableAuto') : t('tv.delete')}
                            onSelect={() => (close(false), setRemoving(c))}
                          />
                        </>
                      )}
                    </Menu>
                  </span>
                </ListRow>
              ))}
            </GroupedSection>
          )}
      </div>

      {edit && (
        <ChannelEditor
          channel={edit.channel}
          me={me}
          onClose={() => {
            setEdit(null)
            if (params.get('new')) setParams({}, { replace: true })
          }}
          onLogoChanged={() => load(true)}
          onSaved={(_c, warning) => {
            setEdit(null)
            if (warning) setToast(warning)
            if (params.get('new')) setParams({}, { replace: true })
            load(true)
          }}
        />
      )}
      {removing && (
        <Sheet
          title={t('tv.deleteTitle', { name: removing.name })}
          message={t('tv.deleteMessage')}
          icon={<Icon name="trash" size={34} strokeWidth={1.6} />}
          onCancel={() => setRemoving(null)}
          actions={[
            { label: t('common.cancel'), kind: 'cancel', run: () => setRemoving(null) },
            {
              label: removing.auto ? t('tv.disableAuto') : t('tv.delete'),
              kind: 'destructive',
              run: () => {
                const c = removing
                setRemoving(null)
                deleteChannel(c.id).then(
                  () => load(true),
                  (e: unknown) => setToast(errorText(e, t('common.genericError'))),
                )
              },
            },
          ]}
        />
      )}
      {toast && <Toast>{toast}</Toast>}
    </div>
  )
}
