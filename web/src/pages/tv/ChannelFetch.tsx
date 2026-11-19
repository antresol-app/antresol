// «С докачкой» in the channel editor (an admin's, rules.fetch): the
// channel's titles in order — movies and series (or one season), searched
// on TMDB, on disk or not — the disk it may take, of its own and of all
// such channels together, how long a programme stays after it aired, and,
// for a saved channel, where each programme is: on disk, being fetched for
// its slot, waiting, cleaned up. The server fetches what is not on disk
// before it airs and removes it after, unless someone keeps it.
import { Fragment, useEffect, useMemo, useState } from 'react'
import { errorText } from '../../api/client'
import {
  FETCH_DEFAULT_CHANNEL_GB,
  FETCH_DEFAULT_GRACE_HOURS,
  getChannelFetch,
  getChannelFetchSeasons,
  searchChannelFetch,
  type ChannelFetchFound,
  type ChannelFetchProgramme,
  type ChannelFetchRules,
  type ChannelFetchStatus,
  type ChannelFetchTitle,
} from '../../api/channels'
import { Icon } from '../../components/Icon'
import { formatPercent, useT } from '../../i18n'
import { formatSize } from '../../state/format'
import { IconButton } from '../../ui/IconButton'
import { GroupedSection, ListRow } from '../../ui/List'
import { NumberField } from '../../ui/NumberField'
import { ProgressBar } from '../../ui/ProgressBar'
import { SortableList, SortableRowText } from '../../ui/SortableList'
import { TextField } from '../../ui/TextField'
import styles from './Tv.module.css'

const titleKey = (x: ChannelFetchTitle) => `${x.kind}:${x.tmdbId}:${x.season ?? 0}`

export interface ChannelFetchProps {
  value: ChannelFetchRules
  onChange: (v: ChannelFetchRules) => void
  /** The saved channel (null: a new one, no status yet). */
  channelId: number | null
  /** The disk all such channels may take (GB) and its setter: saved with the channel. */
  globalGB: number
  onGlobalGB: (gb: number) => void
  /** Nothing chosen yet and the editor wants something. */
  need: boolean
}

export function ChannelFetch({ value, onChange, channelId, globalGB, onGlobalGB, need }: ChannelFetchProps) {
  const t = useT()
  const [status, setStatus] = useState<ChannelFetchStatus | null>(null)
  useEffect(() => {
    if (channelId == null) return
    const ac = new AbortController()
    getChannelFetch(channelId, ac.signal).then(setStatus, () => undefined)
    return () => ac.abort()
  }, [channelId])

  const detailOf = (x: ChannelFetchTitle) =>
    [t(`tv.picks.kind.${x.kind}`), x.year, x.kind === 'series' ? (x.season ? t('tv.fetch.season', { n: x.season }) : t('tv.fetch.allSeasons')) : '']
      .filter(Boolean)
      .join(' · ')
  const has = useMemo(() => new Set(value.titles.map(titleKey)), [value.titles])
  const add = (x: ChannelFetchTitle) => {
    if (!has.has(titleKey(x))) onChange({ ...value, titles: [...value.titles, x] })
  }

  return (
    <>
      <GroupedSection
        title={
          <>
            {t('tv.picks.title')} · <span className={styles.titleCount}>{t('tv.picks.count', { count: value.titles.length })}</span>
          </>
        }
        footer={need ? t('tv.fetch.need') : t('tv.fetch.about')}
      >
        <ListRow label={t('tv.picks.listLabel')} hint={t('tv.fetch.listHint')} wide>
          <SortableList
            label={t('tv.picks.title')}
            items={value.titles}
            getKey={titleKey}
            itemLabel={(x) => x.title ?? String(x.tmdbId)}
            renderItem={(x) => (
              <SortableRowText leading={<Icon name={x.kind === 'series' ? 'tv' : 'film'} size={16} />} title={x.title ?? String(x.tmdbId)} detail={detailOf(x)} />
            )}
            onChange={(titles) => onChange({ ...value, titles })}
            removable
            moveButtons
            empty={<p className={styles.picksEmpty}>{t('tv.fetch.empty')}</p>}
          />
        </ListRow>
      </GroupedSection>

      <GroupedSection title={t('tv.picks.add')}>
        <FetchSearch has={has} onAdd={add} />
      </GroupedSection>

      <GroupedSection title={t('tv.fetch.disk')} footer={t('tv.fetch.diskFooter')}>
        <ListRow
          label={t('tv.fetch.channelBudget')}
          hint={status ? t('tv.fetch.used', { used: formatSize(status.channel.used), budget: formatSize(status.channel.budget) }) : undefined}
        >
          <NumberField
            label={t('tv.fetch.channelBudget')}
            value={value.budgetGB || FETCH_DEFAULT_CHANNEL_GB}
            onChange={(gb) => onChange({ ...value, budgetGB: gb })}
            unit={t('tv.fetch.gb')}
            min={1}
            max={100000}
            chars={3}
          />
        </ListRow>
        <ListRow
          label={t('tv.fetch.globalBudget')}
          hint={
            status
              ? t('tv.fetch.usedAll', { used: formatSize(status.global.used), budget: formatSize(status.global.budget) })
              : t('tv.fetch.globalHint')
          }
        >
          <NumberField label={t('tv.fetch.globalBudget')} value={globalGB} onChange={onGlobalGB} unit={t('tv.fetch.gb')} min={1} max={100000} chars={3} />
        </ListRow>
        <ListRow label={t('tv.fetch.grace')} hint={t('tv.fetch.graceHint')}>
          <NumberField
            label={t('tv.fetch.grace')}
            value={value.graceHours || FETCH_DEFAULT_GRACE_HOURS}
            onChange={(h) => onChange({ ...value, graceHours: h })}
            unit={t('tv.fetch.hours')}
            min={1}
            max={720}
            chars={3}
          />
        </ListRow>
        {status && !status.available && <ListRow label={<span className={styles.formErrorInline}>{t('tv.fetch.unavailable')}</span>} />}
      </GroupedSection>

      {status && status.programmes.length > 0 && <FetchProgrammes list={status.programmes} />}
    </>
  )
}

function FetchSearch({ has, onAdd }: { has: Set<string>; onAdd: (x: ChannelFetchTitle) => void }) {
  const t = useT()
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<ChannelFetchFound[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  // a series whose seasons are shown, and them
  const [open, setOpen] = useState<{ id: number; seasons: number[] | null } | null>(null)
  useEffect(() => {
    const query = q.trim()
    if (!query) {
      setHits(null)
      setBusy(false)
      setError('')
      return
    }
    const ac = new AbortController()
    setBusy(true)
    const id = window.setTimeout(() => {
      searchChannelFetch(query, ac.signal).then(
        (r) => {
          setHits(r)
          setError('')
          setBusy(false)
        },
        (e: unknown) => {
          if (ac.signal.aborted) return
          setError(errorText(e, t('common.loadFailed')))
          setBusy(false)
        },
      )
    }, 300)
    return () => {
      window.clearTimeout(id)
      ac.abort()
    }
  }, [q, t])
  const toggle = (h: ChannelFetchFound) => {
    if (open?.id === h.tmdbId) return setOpen(null)
    setOpen({ id: h.tmdbId, seasons: null })
    getChannelFetchSeasons(h.tmdbId).then(
      (seasons) => setOpen((o) => (o?.id === h.tmdbId ? { id: h.tmdbId, seasons } : o)),
      () => setOpen((o) => (o?.id === h.tmdbId ? { id: h.tmdbId, seasons: [] } : o)),
    )
  }
  const addButton = (x: ChannelFetchTitle, name: string) => {
    const added = has.has(titleKey(x))
    return (
      <IconButton
        variant="plain"
        size="sm"
        icon={added ? 'checkCircle' : 'plusCircle'}
        className={added ? styles.pickAdded : styles.pickAdd}
        label={added ? t('tv.picks.added', { name }) : t('tv.picks.addOne', { name })}
        disabled={added}
        onClick={() => onAdd(x)}
      />
    )
  }
  return (
    <>
      <ListRow
        label={
          <div className={styles.pickField}>
            <TextField
              type="search"
              size="small"
              label={t('tv.fetch.searchField')}
              hideLabel
              placeholder={t('tv.fetch.searchField')}
              value={q}
              onChange={(e) => setQ(e.target.value)}
              onClear={() => setQ('')}
              autoComplete="off"
            />
          </div>
        }
      />
      {error && <ListRow label={<span className={styles.formError}>{error}</span>} />}
      {!error && busy && hits === null && <ListRow label={<span className={styles.pickNote}>{t('tv.picks.searching')}</span>} />}
      {!error && hits?.length === 0 && <ListRow label={<span className={styles.pickNote}>{t('tv.picks.noHits')}</span>} />}
      {hits?.map((h) => {
        const base: ChannelFetchTitle = { kind: h.kind, tmdbId: h.tmdbId, title: h.title, year: h.year }
        const expanded = open?.id === h.tmdbId
        return (
          <Fragment key={`${h.kind}:${h.tmdbId}`}>
            <ListRow
              leading={
                <span className={styles.pickGlyph} aria-hidden>
                  <Icon name={h.kind === 'movie' ? 'film' : 'tv'} size={16} />
                </span>
              }
              label={<span className={styles.pickName}>{h.title}</span>}
              hint={[t(`tv.picks.kind.${h.kind}`), h.year, h.kind === 'series' ? t('tv.fetch.allSeasons') : ''].filter(Boolean).join(' · ')}
            >
              {addButton(base, h.title)}
              {h.kind === 'series' && (
                <IconButton
                  variant="plain"
                  size="sm"
                  icon={expanded ? 'chevronUp' : 'chevronDown'}
                  label={t('tv.fetch.seasons', { name: h.title })}
                  onClick={() => toggle(h)}
                />
              )}
            </ListRow>
            {expanded && open.seasons === null && <ListRow label={<span className={styles.pickNote}>{t('tv.picks.loading')}</span>} />}
            {expanded &&
              open.seasons?.map((n) => {
                const name = `${h.title} · ${t('tv.fetch.season', { n })}`
                return (
                  <ListRow key={n} leading={<span className={`${styles.pickGlyph} ${styles.fetchSeason}`} aria-hidden />} label={<span className={styles.pickName}>{t('tv.fetch.season', { n })}</span>}>
                    {addButton({ ...base, season: n }, name)}
                  </ListRow>
                )
              })}
          </Fragment>
        )
      })}
    </>
  )
}

/** Where each programme of a saved channel is. */
function FetchProgrammes({ list }: { list: ChannelFetchProgramme[] }) {
  const t = useT()
  const time = (iso: string) => new Date(iso).toLocaleString(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' })
  const shown = list.slice(0, 200)
  return (
    <GroupedSection title={t('tv.fetch.programmes')}>
      {shown.map((p, i) => {
        const name = p.kind === 'episode' ? `${t('tv.fetch.episode', { s: p.season ?? 0, e: p.episode ?? 0 })}${p.episodeTitle ? ` · ${p.episodeTitle}` : ''}` : p.title
        const fetching = p.state === 'requested'
        const share = fetching && p.size ? (p.done ?? 0) / p.size : 0
        const state = [
          fetching && p.size ? t('tv.fetch.state.fetching', { percent: formatPercent(Math.floor(share * 100)) }) : t(`tv.fetch.state.${p.state}`),
          p.airs ? t('tv.fetch.airs', { time: time(p.airs) }) : '',
          p.error ?? '',
        ]
          .filter(Boolean)
          .join(' · ')
        return (
          <ListRow key={i} label={<span className={styles.pickName}>{name}</span>} hint={p.kind === 'episode' ? p.title : undefined} status={state}>
            {fetching && p.size ? <ProgressBar value={share} label={name} className={styles.fetchProgress} /> : null}
          </ListRow>
        )
      })}
    </GroupedSection>
  )
}
