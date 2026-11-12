// «ТВ» (/tv): the channels as a television has them — ONE list in number
// order (themes and the profile's favourites are only filters), each row
// with what is on, how far it is and what follows; or «Телегид», the
// programme guide grid. The channel the profile watched last is marked
// (its number in the accent). A wide window shows the details of the channel under the
// pointer beside the list. A row opens the live player, which zaps within
// the list it was opened from. «Управление каналами» is in «⋯».
import { useEffect, useMemo, useState, type CSSProperties } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { APP_NAME } from '../../brand'
import { channelsOpened } from '../../api/iptv'
import { Icon } from '../../components/Icon'
import { useT } from '../../i18n'
import { useIsCompact, useMediaQuery } from '../../state/useMediaQuery'
import { Button, ButtonLink } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { Menu, MenuItem } from '../../ui/Menu'
import { SegmentedControl } from '../../ui/SegmentedControl'
import { Toast } from '../../ui/Toast'
import { ChannelPreview } from './ChannelPreview'
import { ChannelRow } from './ChannelRow'
import { Guide } from './Guide'
import { applyFilter, filterParam, numDigits, parseFilter, themesOf, validFilter, type ChannelFilter } from './model'
import { useChannelList, useNow } from './useChannels'
import styles from './Tv.module.css'

type View = 'list' | 'guide'

/** The player's address for a channel, carrying the list it zaps through. */
export function playerPath(id: number, f: ChannelFilter): string {
  const p = filterParam(f)
  return `/tv/${id}${p ? `?f=${encodeURIComponent(p)}` : ''}`
}

export function Tv() {
  const t = useT()
  const now = useNow()
  const compact = useIsCompact()
  const wide = useMediaQuery('(min-width: 1100px)')
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const { state, data, load, toggleFavorite, favError, clearFavError } = useChannelList()
  useEffect(() => {
    document.title = `${t('tv.title')} — ${APP_NAME}`
  }, [t])
  // «при открытии»: the server refreshes the external sources that ask for it
  useEffect(() => {
    channelsOpened().catch(() => undefined)
  }, [])
  useEffect(() => {
    if (!favError) return
    const id = window.setTimeout(clearFavError, 4000)
    return () => window.clearTimeout(id)
  }, [favError, clearFavError])

  const channels = useMemo(() => data?.channels ?? [], [data])
  const view: View = params.get('view') === 'guide' ? 'guide' : 'list'
  // the filter and the view are in the address, so «Назад» from a channel comes back to them
  const filter = validFilter(channels, parseFilter(params.get('f')))
  const themes = themesOf(channels)
  const shown = applyFilter(channels, filter)
  const setParam = (k: string, v: string) =>
    setParams(
      (p) => {
        const n = new URLSearchParams(p)
        if (v) n.set(k, v)
        else n.delete(k)
        return n
      },
      { replace: true },
    )
  const lastId = data?.lastChannel ?? null
  const [previewId, setPreviewId] = useState<number | null>(null)
  const preview = shown.find((c) => c.id === previewId) ?? shown.find((c) => c.id === lastId) ?? shown[0]
  const favorites = useMemo(() => Object.fromEntries(channels.map((c) => [c.id, Boolean(c.favorite)])), [channels])
  const logos = useMemo(() => Object.fromEntries(channels.map((c) => [c.id, c.logoUrl])), [channels])

  const filterKey = filterParam(filter) || 'all'
  const filters = [
    { value: 'all', label: t('tv.filterAll') },
    { value: 'fav', label: t('tv.filterFav') },
    ...themes.map((th) => ({ value: `c:${th}`, label: th })),
  ]

  const manage = data?.canCreate && (
    <Menu label={t('tv.more')} placement="bottom-end" trigger={(p) => <IconButton {...p} icon="ellipsis" label={t('tv.more')} size={compact ? 'sm' : 'md'} />}>
      {(close) => (
        <>
          <MenuItem icon={<Icon name="sliders" size={16} />} label={t('tv.manage')} onSelect={() => (close(false), navigate('/tv/manage'))} />
          <MenuItem icon={<Icon name="plus" size={16} />} label={t('tv.newChannel')} onSelect={() => (close(false), navigate('/tv/manage?new=1'))} />
        </>
      )}
    </Menu>
  )

  return (
    <div className={styles.page} data-view={view}>
      <header className={styles.header}>
        <h1 className={styles.title}>{t('tv.title')}</h1>
        {channels.length > 0 && (
          <div className={styles.viewSwitch}>
            <SegmentedControl
              label={t('tv.view')}
              size="small"
              value={view}
              onChange={(v) => setParam('view', v === 'guide' ? 'guide' : '')}
              segments={[
                { value: 'list', label: t('tv.channels') },
                { value: 'guide', label: t('tv.guide') },
              ]}
            />
          </div>
        )}
        <div className={styles.headerEnd}>
          {manage}
        </div>
      </header>

      {channels.length > 0 && (
        <div className={styles.filters}>
          <SegmentedControl label={t('tv.filter')} row align="start" value={filterKey} onChange={(v) => setParam('f', v === 'all' ? '' : v)} segments={filters} />
        </div>
      )}

      {state.status === 'error' && (
        <div className={styles.empty} role="alert">
          <Icon name="warning" size={40} strokeWidth={1.4} />
          <p>{state.message}</p>
          <Button variant="secondary" onClick={() => load()}>
            {t('common.retry')}
          </Button>
        </div>
      )}
      {data && channels.length === 0 && (
        <div className={styles.empty}>
          <Icon name="broadcast" size={48} strokeWidth={1.4} />
          <h2>{t('tv.empty')}</h2>
          {data.canCreate && (
            <>
              <p>{data.canManage ? t('tv.emptyAdmin') : t('tv.emptyOthers')}</p>
              <ButtonLink to="/tv/manage?new=1" icon="plus">
                {t('tv.newChannel')}
              </ButtonLink>
            </>
          )}
        </div>
      )}
      {data && channels.length > 0 && shown.length === 0 && filter.kind === 'fav' && (
        <div className={styles.empty}>
          <Icon name="star" size={44} strokeWidth={1.4} />
          <h2>{t('tv.noFavorites')}</h2>
          <p>{t('tv.noFavoritesHint')}</p>
        </div>
      )}

      {view === 'list' && shown.length > 0 && (
        <div className={styles.split}>
          <ul className={styles.list} aria-label={t('tv.channels')} style={{ '--num-digits': numDigits(shown) } as CSSProperties}>
            {shown.map((c, i) => (
              <ChannelRow
                key={c.id}
                channel={c}
                now={now}
                to={playerPath(c.id, filter)}
                last={c.id === lastId}
                selected={wide && preview?.id === c.id}
                eager={i < 8}
                logoSlot={shown.some((x) => x.logoUrl)}
                onFavorite={(on) => toggleFavorite(c.id, on)}
                onPreview={wide ? () => setPreviewId(c.id) : undefined}
              />
            ))}
          </ul>
          {wide && preview && (
            <div className={styles.previewCol}>
              <ChannelPreview channel={preview} now={now} to={playerPath(preview.id, filter)} onFavorite={(on) => toggleFavorite(preview.id, on)} />
            </div>
          )}
        </div>
      )}
      {view === 'guide' && channels.length > 0 && (shown.length > 0 || filter.kind !== 'fav') && (
        <Guide filter={filter} favorites={favorites} onFavorite={toggleFavorite} playerTo={(id) => playerPath(id, filter)} logos={logos} />
      )}
      {favError && <Toast tone="error">{favError}</Toast>}
    </div>
  )
}
