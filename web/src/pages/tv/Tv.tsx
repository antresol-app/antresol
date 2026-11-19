// «ТВ» (/tv): the channels as a television has them — ONE list in number
// order (the profile's starred ones first when it wants them so: «Мои
// настройки → Избранные каналы первыми»), each row with what is on and how
// far it is; or «Телегид», the programme guide grid. A few categories are
// segments over the list; a big list's (an IPTV playlist: the AI helper's
// topics, the sources, the playlist's groups) are a level of their own:
// /tv lists them with «Избранное» and «Все каналы», a category's channels
// are the next level (?f=…) with «Категории» back. The list renders only
// the rows near the screen (thousands with a playlist). The channel the
// profile watched last is marked (its number in the accent). A wide window
// shows the details of the channel under the pointer beside the list. A
// row opens the live player, which zaps within the list it was opened
// from. «Управление каналами» is in «⋯».
//
// A wide window has TiviMate's layout: the categories as a left column
// (ChannelCategories' rail: «Избранное» first, «Для взрослых» last), the
// channels of the chosen one with what is on, and the details of the channel
// in focus. The keyboard (a remote) moves through it: ↑/↓ in a column, →
// from the categories into the channels, ← back, Enter opens the channel.
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent } from 'react'
import { useNavigate, useNavigationType, useSearchParams } from 'react-router'
import { APP_NAME } from '../../brand'
import { channelsOpened } from '../../api/iptv'
import { Icon } from '../../components/Icon'
import { useT } from '../../i18n'
import { useSession } from '../../state/session'
import { useIsCompact, useMediaQuery } from '../../state/useMediaQuery'
import { Button, ButtonLink } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { Menu, MenuItem } from '../../ui/Menu'
import { SegmentedControl } from '../../ui/SegmentedControl'
import { Toast } from '../../ui/Toast'
import { VirtualList } from '../../ui/VirtualList'
import { ChannelCategories } from './ChannelCategories'
import { ChannelPreview } from './ChannelPreview'
import { ChannelRow } from './ChannelRow'
import { Guide } from './Guide'
import { channelsManagePath } from './manageBase'
import { applyFilter, categoryList, filterParam, isExclusive, numDigits, parseFilter, validFilter, type ChannelFilter } from './model'
import { useChannelList, useNow } from './useChannels'
import styles from './Tv.module.css'

type View = 'list' | 'guide'

/** Categories shown as segments; more are a level of their own (ChannelCategories). */
const MAX_INLINE = 5

/** A row's height (px): the phone's and the wide layout's (Tv.module.css draws the row inside it). */
const ROW_H = { compact: 72, wide: 84 }

/** The player's address for a channel, carrying the list it zaps through. */
export function playerPath(id: number, f: ChannelFilter): string {
  const p = filterParam(f)
  return `/tv/${id}${p ? `?f=${encodeURIComponent(p)}` : ''}`
}

export function Tv() {
  const t = useT()
  const { user } = useSession()
  const now = useNow()
  const compact = useIsCompact()
  const wide = useMediaQuery('(min-width: 1100px)')
  // the details pane beside the rail and the list needs a wider window
  const widest = useMediaQuery('(min-width: 1360px)')
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
  const favFirst = Boolean(data?.favoritesFirst)
  const view: View = params.get('view') === 'guide' ? 'guide' : 'list'
  // the filter and the view are in the address, so «Назад» from a channel comes back to them
  const fParam = params.get('f')
  const filter = useMemo(() => validFilter(channels, parseFilter(fParam)), [channels, fParam])
  // a level chosen (a category, «Категории» back) opens at its top; Back keeps where it was
  const navType = useNavigationType()
  useEffect(() => {
    if (navType === 'PUSH') window.scrollTo(0, 0)
  }, [fParam, navType])
  const categories = useMemo(() => categoryList(channels, data?.categories), [channels, data])
  const shown = useMemo(() => applyFilter(channels, filter, favFirst), [channels, filter, favFirst])
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
  // «Все» and «Избранное» leave the exclusive (adult) channels out
  const favCount = useMemo(() => channels.filter((c) => c.favorite && !isExclusive(c)).length, [channels])
  const allCount = useMemo(() => channels.filter((c) => !isExclusive(c)).length, [channels])

  // a few categories (an own list's themes) stay one tap away as segments; a
  // big list's are the first level (no ?f): the list of them
  // a wide window: the categories are the left column (TiviMate)
  const rail = wide && categories.length > 0
  const inline = !rail && categories.length <= MAX_INLINE
  const level1 = !rail && !inline && !fParam
  const showPreview = rail ? widest : wide
  const filterKey = filterParam(filter)
  const filters = [
    { value: 'all', label: t('tv.filterAll') },
    { value: 'fav', label: t('tv.filterFav') },
    ...(inline ? categories.map((c) => ({ value: `k:${c.id}`, label: c.name })) : []),
  ]
  // an address of this page with another filter (the view kept)
  const href = useCallback(
    (f: string) => {
      const n = new URLSearchParams(params)
      if (f) n.set('f', f)
      else n.delete('f')
      const q = n.toString()
      return `/tv${q ? `?${q}` : ''}`
    },
    [params],
  )
  // the remote's moves in the channel list: ↑/↓ a row (scrolled into sight:
  // the list renders only the rows near the screen), ← to the categories
  const listRef = useRef<HTMLDivElement>(null)
  const rowH = compact ? ROW_H.compact : ROW_H.wide
  const focusRow = useCallback(
    (i: number) => {
      const el = listRef.current
      if (!el || i < 0 || i >= shown.length) return
      const top = el.getBoundingClientRect().top + window.scrollY + i * rowH
      const margin = rowH * 2
      if (top < window.scrollY + margin) window.scrollTo({ top: Math.max(0, top - margin) })
      else if (top + rowH > window.scrollY + window.innerHeight - margin) window.scrollTo({ top: top + rowH + margin - window.innerHeight })
      let tries = 0
      const tryFocus = () => {
        const a = el.querySelector<HTMLElement>(`[data-index="${i}"] a`)
        if (a) a.focus({ preventScroll: true })
        else if (tries++ < 4) window.requestAnimationFrame(tryFocus)
      }
      tryFocus()
    },
    [shown.length, rowH],
  )
  const enterList = useCallback(() => {
    const at = shown.findIndex((c) => c.id === (previewId ?? lastId))
    focusRow(at < 0 ? 0 : at)
  }, [shown, previewId, lastId, focusRow])
  const onListKey = (e: KeyboardEvent<HTMLDivElement>) => {
    const item = (document.activeElement as HTMLElement | null)?.closest<HTMLElement>('[data-index]')
    if (!item || !listRef.current?.contains(item)) return
    const i = Number(item.dataset.index)
    let next = -1
    if (e.key === 'ArrowDown') next = i + 1
    else if (e.key === 'ArrowUp') next = i - 1
    else if (e.key === 'PageDown') next = Math.min(shown.length - 1, i + 8)
    else if (e.key === 'PageUp') next = Math.max(0, i - 8)
    else if (e.key === 'ArrowLeft' && rail) {
      e.preventDefault()
      document.querySelector<HTMLElement>(`.${styles.rail} a[aria-current]`)?.focus()
      return
    } else return
    e.preventDefault()
    focusRow(next)
  }

  const filterName = filter.kind === 'fav' ? t('tv.filterFav') : filter.kind === 'cat' ? (categories.find((c) => c.id === filter.id)?.name ?? '') : t('tv.allChannels')

  // an admin manages the channels in «Настройки», anyone else here in «ТВ»
  const managePath = channelsManagePath(user?.isAdmin)
  const manage = data?.canCreate && (
    <Menu label={t('tv.more')} placement="bottom-end" trigger={(p) => <IconButton {...p} icon="ellipsis" label={t('tv.more')} size={compact ? 'sm' : 'md'} />}>
      {(close) => (
        <>
          <MenuItem icon={<Icon name="sliders" size={16} />} label={t('tv.manage')} onSelect={() => (close(false), navigate(managePath))} />
          <MenuItem icon={<Icon name="plus" size={16} />} label={t('tv.newChannel')} onSelect={() => (close(false), navigate(`${managePath}?new=1`))} />
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
        <div className={styles.headerEnd}>{manage}</div>
      </header>

      {channels.length > 0 && inline && (
        <div className={styles.filters}>
          <SegmentedControl label={t('tv.filter')} row align="start" value={filterKey} onChange={(v) => setParam('f', v === 'all' ? '' : v)} segments={filters} />
        </div>
      )}
      {channels.length > 0 && !rail && !inline && !level1 && (
        <div className={styles.level}>
          <ButtonLink to={href('')} variant="plain" size="small" icon="chevronLeft" className={styles.levelBack}>
            {t('tv.categories')}
          </ButtonLink>
          <h2 className={styles.levelTitle}>{filterName}</h2>
        </div>
      )}
      {data && level1 && channels.length > 0 && <ChannelCategories categories={categories} all={allCount} favorites={favCount} href={href} />}

      <div className={rail ? styles.railLayout : styles.noRail}>
        {rail && channels.length > 0 && (
          <div className={styles.railCol}>
            <ChannelCategories variant="rail" categories={categories} all={allCount} favorites={favCount} href={href} current={filterKey} onEnterList={enterList} />
          </div>
        )}
        <div className={rail ? styles.railMain : styles.noRail}>
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
                  <ButtonLink to={`${managePath}?new=1`} icon="plus">
                    {t('tv.newChannel')}
                  </ButtonLink>
                </>
              )}
            </div>
          )}
          {data && !level1 && channels.length > 0 && shown.length === 0 && filter.kind === 'fav' && (
            <div className={styles.empty}>
              <Icon name="star" size={44} strokeWidth={1.4} />
              <h2>{t('tv.noFavorites')}</h2>
              <p>{t('tv.noFavoritesHint')}</p>
            </div>
          )}

          {view === 'list' && !level1 && shown.length > 0 && (
            <div className={styles.split} data-preview={showPreview || undefined}>
              <div ref={listRef} style={{ '--num-digits': numDigits(shown) } as CSSProperties} onKeyDown={onListKey}>
                <VirtualList
                  count={shown.length}
                  itemHeight={compact ? ROW_H.compact : ROW_H.wide}
                  itemKey={(i) => shown[i]!.id}
                  label={t('tv.channels')}
                  className={styles.list}
                  itemClassName={styles.rowItem}
                  renderItem={(i) => {
                    const c = shown[i]!
                    return (
                      <ChannelRow
                        channel={c}
                        now={now}
                        to={playerPath(c.id, filter)}
                        last={c.id === lastId}
                        selected={showPreview && preview?.id === c.id}
                        eager={i < 8}
                        onFavorite={toggleFavorite}
                        onPreview={showPreview || rail ? setPreviewId : undefined}
                      />
                    )
                  }}
                />
              </div>
              {showPreview && preview && (
                <div className={styles.previewCol}>
                  <ChannelPreview channel={preview} now={now} to={playerPath(preview.id, filter)} onFavorite={(on) => toggleFavorite(preview.id, on)} />
                </div>
              )}
            </div>
          )}
          {view === 'guide' && !level1 && channels.length > 0 && (shown.length > 0 || filter.kind !== 'fav') && (
            <Guide filter={filter} favFirst={favFirst} favorites={favorites} onFavorite={toggleFavorite} playerTo={(id) => playerPath(id, filter)} logos={logos} />
          )}
        </div>
      </div>
      {favError && <Toast tone="error">{favError}</Toast>}
    </div>
  )
}
