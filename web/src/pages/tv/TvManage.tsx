// «Управление каналами» (/tv/manage): the channels the profile may edit — an
// admin's shared ones and its own personal ones, a profile's own — as the
// one numbered list, with its theme and what each plays, on/off, «Изменить»,
// «Удалить» (an automatic channel too: the generator then leaves it alone,
// and «Удалённые каналы» under the list gives it back), «Новый канал» and,
// for admins, «Обновить автоматические каналы».
import { useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { APP_NAME } from '../../brand'
import { errorText } from '../../api/client'
import { deleteChannel, generateChannels, getChannels, restoreChannel, updateChannel, type Channel, type ChannelRules, type RemovedChannel } from '../../api/channels'
import { Icon } from '../../components/Icon'
import { LoadFailed } from '../../components/LoadFailed'
import { t as tr, useT } from '../../i18n'
import { useSession } from '../../state/session'
import { useLoad } from '../../state/useLoad'
import { Badge } from '../../ui/Badge'
import { Button } from '../../ui/Button'
import { EmptyState } from '../../ui/EmptyState'
import { IconButton } from '../../ui/IconButton'
import { GroupedSection, ListRow } from '../../ui/List'
import { Menu, MenuItem, MenuSeparator } from '../../ui/Menu'
import { Sheet } from '../../ui/Sheet'
import { Toast } from '../../ui/Toast'
import { Toggle } from '../../ui/Toggle'
import { ChannelEditor } from './ChannelEditor'
import { ChannelLogo } from './ChannelLogo'
import { useChannelsFrame } from './manageBase'
import { byNumber } from './model'
import sourcesStyles from './sources/Sources.module.css'
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
  const frame = useChannelsFrame()
  const Title = frame.embedded ? 'h2' : 'h1'
  const [edit, setEdit] = useState<{ channel: Channel | null } | null>(params.get('new') ? { channel: null } : null)
  const [removing, setRemoving] = useState<Channel | null>(null)
  const [resetting, setResetting] = useState(false)
  const [toast, setToast] = useState('')
  const [busy, setBusy] = useState(false)
  const [restoring, setRestoring] = useState<number | null>(null)
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
  const generate = (reset = false) => {
    setResetting(false)
    setBusy(true)
    generateChannels(reset)
      .then((r) => {
        setToast(t('tv.generated', r))
        load(true)
      })
      .catch((e: unknown) => setToast(errorText(e, t('common.genericError'))))
      .finally(() => setBusy(false))
  }

  const remove = (c: Channel) => {
    setRemoving(null)
    deleteChannel(c.id).then(
      () => {
        setToast(t(c.auto ? 'tv.deletedAuto' : 'tv.deleted', { name: c.name }))
        load(true)
      },
      (e: unknown) => {
        setToast(errorText(e, t('common.genericError')))
        load(true)
      },
    )
  }
  const restore = (c: RemovedChannel) => {
    setRestoring(c.id)
    restoreChannel(c.id)
      .then((r) => setToast(t(r.restored ? 'tv.restored' : 'tv.restoredLater', { name: c.name })))
      .catch((e: unknown) => setToast(errorText(e, t('common.genericError'))))
      .finally(() => {
        setRestoring(null)
        load(true)
      })
  }
  const removed = data?.removed ?? []
  // what the delete does, by what the channel is: an automatic one is only
  // remembered as deleted (it can be given back), one with fetching also lets
  // go of the files fetched for it
  const deleteMessage = (c: Channel) => {
    if (c.auto) return t('tv.deleteMessageAuto')
    if (c.rules?.fetch?.titles?.length) return t('tv.deleteMessageFetch')
    return t('tv.deleteMessage')
  }

  return (
    <div className={frame.page(styles.page)}>
      <div className={frame.column(styles.column)}>
        <header className={frame.header(styles.columnHeader)}>
          <Title className={frame.title(styles.title)}>{t('tv.manageTitle')}</Title>
          <div className={styles.actions}>
            <Button icon="plus" onClick={() => setEdit({ channel: null })}>
              {t('tv.newChannel')}
            </Button>
            {me.isAdmin && (
              <Menu label={t('tv.more')} placement="bottom-end" trigger={(p) => <IconButton {...p} icon="ellipsis" label={t('tv.more')} loading={busy} />}>
                {(close) => (
                  <>
                    <MenuItem icon={<Icon name="restart" size={16} />} label={t('tv.generate')} onSelect={() => (close(false), generate())} />
                    <MenuItem icon={<Icon name="trash" size={16} />} label={t('tv.resetAuto')} onSelect={() => (close(false), setResetting(true))} />
                  </>
                )}
              </Menu>
            )}
          </div>
        </header>

        {me.isAdmin && (
          <GroupedSection className={styles.sourcesEntry}>
            <ListRow
              className={sourcesStyles.linkRow}
              leading={
                <span className={sourcesStyles.glyph}>
                  <Icon name="broadcast" size={18} />
                </span>
              }
              label={
                <Link to={`${frame.base}/sources`} className={sourcesStyles.rowLink}>
                  {t('iptv.entry')}
                </Link>
              }
              hint={t('iptv.entryHint')}
            >
              <Icon name="chevronRight" size={16} className={sourcesStyles.chevron} />
            </ListRow>
            <ListRow
              className={sourcesStyles.linkRow}
              leading={
                <span className={sourcesStyles.glyph}>
                  <Icon name="film" size={18} />
                </span>
              }
              label={
                <Link to={`${frame.base}/franchises`} className={sourcesStyles.rowLink}>
                  {t('tv.franchises.entry')}
                </Link>
              }
              hint={t('tv.franchises.entryHint')}
            >
              <Icon name="chevronRight" size={16} className={sourcesStyles.chevron} />
            </ListRow>
          </GroupedSection>
        )}
        {state.status === 'error' && (
          <LoadFailed title={t('common.loadFailed')} message={state.message} retry={t('common.retry')} onRetry={() => load()} />
        )}
        {data && data.channels.length === 0 && (
          <EmptyState icon={<Icon name="broadcast" size={48} strokeWidth={1.4} />} title={t('tv.empty')} hint={me.isAdmin ? t('tv.emptyAdmin') : t('tv.emptyOthers')} />
        )}
        {data && data.channels.length > 0 && (
            <GroupedSection title={t('tv.channels')}>
              {byNumber(data.channels).map((c) => (
                <ListRow
                  key={c.id}
                  leading={
                    <span className={styles.rowLead}>
                      <span className={styles.rowNumber}>{c.number}</span>
                      {data.channels.some((x) => x.logoUrl) && <ChannelLogo url={c.logoUrl} fallback={c.logoAlt} size="sm" reserve />}
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
                          <MenuItem destructive icon={<Icon name="trash" size={16} />} label={t('tv.delete')} onSelect={() => (close(false), setRemoving(c))} />
                        </>
                      )}
                    </Menu>
                  </span>
                </ListRow>
              ))}
            </GroupedSection>
          )}
        {removed.length > 0 && (
          <GroupedSection title={t('tv.removed')} footer={t('tv.removedHint')} className={styles.removed}>
            {removed.map((c) => (
              <ListRow
                key={c.id}
                label={
                  <span className={styles.rowName}>
                    <span>{c.name}</span>
                    {c.personal && me.isAdmin && <Badge>{t('tv.personal')}</Badge>}
                  </span>
                }
                hint={c.category}
              >
                <Button variant="secondary" size="small" loading={restoring === c.id} disabled={restoring !== null} aria-label={t('tv.restoreNamed', { name: c.name })} onClick={() => restore(c)}>
                  {t('tv.restore')}
                </Button>
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
          message={deleteMessage(removing)}
          icon={<Icon name="trash" size={34} strokeWidth={1.6} />}
          onCancel={() => setRemoving(null)}
          actions={[
            { label: t('common.cancel'), kind: 'cancel', run: () => setRemoving(null) },
            { label: t('tv.delete'), kind: 'destructive', run: () => remove(removing) },
          ]}
        />
      )}
      {resetting && (
        <Sheet
          title={t('tv.resetAutoTitle')}
          message={t('tv.resetAutoMessage')}
          icon={<Icon name="restart" size={34} strokeWidth={1.6} />}
          onCancel={() => setResetting(false)}
          actions={[
            { label: t('common.cancel'), kind: 'cancel', run: () => setResetting(false) },
            { label: t('tv.resetAutoRun'), kind: 'destructive', run: () => generate(true) },
          ]}
        />
      )}
      {toast && <Toast>{toast}</Toast>}
    </div>
  )
}
