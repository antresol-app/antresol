// A source's page (/tv/manage/sources/:id, admin): its name, address and
// status, «Обновить» and «⋯» (settings, the guides' refresh, delete); the
// pending-drop and last-error notices; then the editor's table, after
// tvsieve and the Settings tables of macOS. The whole list loads once and
// filters here: a search field and the state filter on one line, the
// provider's groups as chips with counts, a bulk «Показать все / Скрыть
// все» for the chosen group, then one virtualised row per channel —
// number, logo, name (edited in place), category, how it got its guide,
// why it is shown or hidden, the switch, «⋯». A phone folds the columns
// into the row's second line; a tap opens the channel's sheet.
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { APP_NAME } from '../../../brand'
import { errorText, getUsers } from '../../../api/client'
import {
  confirmDrop,
  deleteSource,
  getSourceChannels,
  getSources,
  patchChannels,
  patchGroup,
  refreshEPG,
  refreshSource,
  rejectDrop,
  updateSource,
  type Source,
  type SourceChannel,
} from '../../../api/iptv'
import type { User } from '../../../api/types'
import { Icon } from '../../../components/Icon'
import { useT } from '../../../i18n'
import { ago } from '../../../state/format'
import { COMPACT_QUERY, useMediaQuery } from '../../../state/useMediaQuery'
import { Badge } from '../../../ui/Badge'
import { Button, ButtonLink } from '../../../ui/Button'
import { FilterChips } from '../../../ui/FilterChips'
import { IconButton } from '../../../ui/IconButton'
import { GroupedSection, ListRow } from '../../../ui/List'
import { Menu, MenuItem, MenuSeparator } from '../../../ui/Menu'
import { Notice } from '../../../ui/Notice'
import { SegmentedControl } from '../../../ui/SegmentedControl'
import { Sheet } from '../../../ui/Sheet'
import { TextField } from '../../../ui/TextField'
import { Toast } from '../../../ui/Toast'
import { Toggle } from '../../../ui/Toggle'
import { VirtualList } from '../../../ui/VirtualList'
import { ChannelLogo } from '../ChannelLogo'
import { ChannelSheet } from './ChannelSheet'
import { epgLabel, whyLabel } from './labels'
import { SourceEditor } from './SourceEditor'
import { DropNotice, num, refreshedText, refreshSummary, sourceAddress } from './SourcesPage'
import styles from './Sources.module.css'

type StateFilter = 'all' | 'shown' | 'hidden' | 'new' | 'gone'

/** The row's height: a list row's; a phone's row has two lines. */
const ROW_WIDE = 52
const ROW_PHONE = 64

const capital = (s: string) => s.charAt(0).toLocaleUpperCase() + s.slice(1)


export function SourceChannels() {
  const t = useT()
  const navigate = useNavigate()
  const { id: idParam } = useParams()
  const id = Number(idParam)
  const phone = useMediaQuery(COMPACT_QUERY)
  const [source, setSource] = useState<Source | null>(null)
  const [xtream, setXtream] = useState(false)
  const [list, setList] = useState<SourceChannel[] | null>(null)
  const [users, setUsers] = useState<User[]>([])
  const [error, setError] = useState('')
  const [q, setQ] = useState('')
  const [group, setGroup] = useState('')
  const [state, setState] = useState<StateFilter>('all')
  const [editing, setEditing] = useState<number | null>(null)
  const [sheet, setSheet] = useState<SourceChannel | null>(null)
  const [settings, setSettings] = useState(false)
  const [removing, setRemoving] = useState(false)
  const [busy, setBusy] = useState('')
  const [toast, setToast] = useState('')

  const loadChannels = useCallback(() => {
    getSourceChannels(id).then(
      (r) => setList(r.channels),
      (e: unknown) => setError(errorText(e, t('iptv.loadFailed'))),
    )
  }, [id, t])
  const loadSource = useCallback(() => {
    getSources().then(
      (r) => {
        setXtream(r.xtreamEnabled)
        const s = r.sources.find((x) => x.id === id)
        if (s) setSource(s)
        else setError(t('iptv.notFound'))
      },
      (e: unknown) => setError(errorText(e, t('iptv.loadFailed'))),
    )
  }, [id, t])
  useEffect(() => {
    loadSource()
    loadChannels()
    getUsers().then(setUsers, () => undefined)
  }, [loadSource, loadChannels])
  useEffect(() => {
    if (source) document.title = `${source.name} — ${APP_NAME}`
  }, [source])
  useEffect(() => {
    if (!toast) return
    const tm = window.setTimeout(() => setToast(''), 4000)
    return () => window.clearTimeout(tm)
  }, [toast])

  const groups = useMemo(() => {
    const m = new Map<string, { total: number; shown: number }>()
    for (const c of list ?? []) {
      const g = m.get(c.group) ?? { total: 0, shown: 0 }
      g.total++
      if (c.shown) g.shown++
      m.set(c.group, g)
    }
    return [...m].map(([name, g]) => ({ name, ...g }))
  }, [list])

  const rows = useMemo(() => {
    const needle = q.trim().toLocaleLowerCase()
    const match = (c: SourceChannel) =>
      !needle ||
      c.name.toLocaleLowerCase().includes(needle) ||
      c.providerName.toLocaleLowerCase().includes(needle) ||
      c.tvgId.toLocaleLowerCase().includes(needle) ||
      c.group.toLocaleLowerCase().includes(needle) ||
      String(c.number) === needle
    const inState = (c: SourceChannel) =>
      state === 'all' ? c.present : state === 'shown' ? c.present && c.shown : state === 'hidden' ? c.present && !c.shown : state === 'new' ? c.new : !c.present
    return (list ?? []).filter((c) => (!group || c.group === group) && inState(c) && match(c))
  }, [list, q, group, state])

  const counts = useMemo(() => {
    const l = (list ?? []).filter((c) => c.present)
    const shown = l.filter((c) => c.shown).length
    return { total: l.length, shown, hidden: l.length - shown, new: l.filter((c) => c.new).length, gone: (list ?? []).length - l.length }
  }, [list])

  const update = (ids: number[], patch: Partial<SourceChannel>) => {
    const set = new Set(ids)
    setList((l) => l?.map((c) => (set.has(c.channelId) ? { ...c, ...patch } : c)) ?? null)
  }
  const failed = (e: unknown) => {
    setToast(errorText(e, t('common.genericError')))
    loadChannels()
  }
  const show = (c: SourceChannel, on: boolean) => {
    update([c.channelId], { shown: on, why: 'manual' })
    patchChannels({ channelIds: [c.channelId], shown: on }).catch(failed)
  }
  const rename = (c: SourceChannel, name: string) => {
    setEditing(null)
    const n = name.trim()
    if (n === c.name || (!n && c.name === c.providerName)) return
    update([c.channelId], { name: n || c.providerName })
    patchChannels({ channelIds: [c.channelId], name: n }).catch(failed)
  }
  const reset = (c: SourceChannel) => patchChannels({ channelIds: [c.channelId], reset: true }).then(loadChannels, failed)
  const showGroup = (on: boolean) => {
    const ids = (list ?? []).filter((c) => c.group === group).map((c) => c.channelId)
    update(ids, { shown: on, why: 'manual' })
    patchGroup({ sourceId: id, group, shown: on }).then(() => loadSource(), failed)
  }

  // the header hides the rows passing behind the top bar only once it is stuck
  const tableRef = useRef<HTMLDivElement>(null)
  const [stuck, setStuck] = useState(false)
  useEffect(() => {
    const check = () => {
      const table = tableRef.current
      const head = table?.firstElementChild
      if (!table || !head) return
      // stuck: the header has left the table's top (a hairline apart at rest)
      setStuck(head.getBoundingClientRect().top - table.getBoundingClientRect().top > 4)
    }
    check()
    window.addEventListener('scroll', check, { passive: true })
    window.addEventListener('resize', check)
    return () => {
      window.removeEventListener('scroll', check)
      window.removeEventListener('resize', check)
    }
  }, [list, group, state, q])

  const run = (what: string, call: Promise<unknown>) => {
    setBusy(what)
    call.finally(() => setBusy(''))
  }
  const refresh = () =>
    run(
      'refresh',
      refreshSource(id).then((r) => {
        setSource(r.source)
        setToast(refreshSummary(t, r))
        loadChannels()
      }, failed),
    )
  const drop = (what: 'confirm' | 'keep') =>
    run(
      what,
      (what === 'confirm' ? confirmDrop(id).then((r) => r.source) : rejectDrop(id)).then((s) => {
        setSource(s)
        setToast(what === 'confirm' ? t('iptv.drop.confirmed') : t('iptv.drop.kept'))
        loadChannels()
      }, failed),
    )

  const chosen = groups.find((g) => g.name === group)
  const dropNotice = source && source.pendingDrop > 0 ? <DropNotice s={source} busy={busy} onConfirm={() => drop('confirm')} onKeep={() => drop('keep')} /> : null

  return (
    <div className={styles.page}>
      <div className={styles.wideColumn}>
        <ButtonLink to="/tv/manage/sources" variant="plain" size="small" icon="chevronLeft" className={styles.back}>
          {t('iptv.backToSources')}
        </ButtonLink>
        <header className={`${styles.header} ${styles.row}`}>
          <div className={styles.headText}>
            <h1 className={styles.title}>{source?.name ?? ' '}</h1>
            {source && (
              <p className={styles.sub}>
                <span className={styles.subLine}>{sourceAddress(source)}</span>
                <span className={styles.nums}>
                  {t('iptv.counts', { total: num(counts.total), shown: num(counts.shown), hidden: num(counts.hidden) })} · <span className={styles.nowrap}>{refreshedText(t, source)}</span>
                </span>
                {counts.new > 0 && (
                  <Badge tone="accent" round className={styles.subBadge}>
                    {t('iptv.newCount', { count: counts.new })}
                  </Badge>
                )}
              </p>
            )}
          </div>
          <div className={styles.headActions}>
            {phone ? (
              <IconButton icon="restart" label={t('iptv.refresh')} loading={busy === 'refresh'} disabled={!!busy} onClick={refresh} />
            ) : (
              <Button variant="secondary" icon="restart" loading={busy === 'refresh'} disabled={!!busy} onClick={refresh}>
                {t('iptv.refresh')}
              </Button>
            )}
            <Menu label={t('iptv.sourceMore')} placement="bottom-end" trigger={(p) => <IconButton {...p} icon="ellipsis" label={t('iptv.sourceMore')} />}>
              {(close) => (
                <>
                  <MenuItem icon={<Icon name="sliders" size={16} />} label={t('iptv.sourceSettings')} onSelect={() => (close(false), setSettings(true))} />
                  {source?.epg.map((g) => (
                    <MenuItem
                      key={g.id}
                      icon={<Icon name="guide" size={16} />}
                      label={t('iptv.epgRefresh')}
                      detail={g.url}
                      onSelect={() => {
                        close()
                        refreshEPG(g.id).then(() => setToast(t('iptv.epgRefreshStarted')), failed)
                      }}
                    />
                  ))}
                  <MenuSeparator />
                  <MenuItem destructive icon={<Icon name="trash" size={16} />} label={t('iptv.sourceDelete')} onSelect={() => (close(false), setRemoving(true))} />
                </>
              )}
            </Menu>
          </div>
        </header>

        {error && (
          <Notice tone="destructive" className={styles.notice}>
            {error}
          </Notice>
        )}
        {dropNotice && <div className={styles.notice}>{dropNotice}</div>}
        {source?.lastError && (
          <Notice className={styles.notice} title={t('iptv.lastErrorTitle')}>
            {capital(source.lastError)}. {source.lastOk ? t('iptv.lastErrorSince', { ago: ago(source.lastOk) }) : ''}
          </Notice>
        )}

        <div className={styles.toolbar}>
          <TextField type="search" size="small" label={t('iptv.table.search')} hideLabel placeholder={t('iptv.table.search')} value={q} onChange={(e) => setQ(e.target.value)} onClear={() => setQ('')} className={styles.search} />
          <SegmentedControl
            label={t('iptv.table.state')}
            size="small"
            value={state}
            onChange={setState}
            segments={[
              { value: 'all', label: t('iptv.table.stateAll') },
              { value: 'shown', label: t('iptv.table.stateShown') },
              { value: 'hidden', label: t('iptv.table.stateHidden') },
              { value: 'new', label: t('iptv.table.stateNew') },
              ...(counts.gone ? [{ value: 'gone' as const, label: t('iptv.table.stateGone') }] : []),
            ]}
          />
        </div>
        <div className={styles.chips}>
          <FilterChips
            label={t('iptv.table.groups')}
            value={group}
            onChange={setGroup}
            formatCount={num}
            chips={[{ value: '', label: t('iptv.table.allGroups'), count: counts.total }, ...groups.map((g) => ({ value: g.name, label: g.name || t('iptv.table.noGroup'), count: g.total }))]}
          />
        </div>

        {chosen && (
          <GroupedSection className={styles.groupBar}>
            <ListRow stack label={t('iptv.group.title', { name: chosen.name || t('iptv.table.noGroup') })} hint={t('iptv.group.counts', { shown: num(chosen.shown), total: num(chosen.total) })}>
              <Button variant="secondary" size="small" disabled={chosen.shown === chosen.total} onClick={() => showGroup(true)}>
                {t('iptv.group.showAll')}
              </Button>
              <Button variant="secondary" size="small" disabled={chosen.shown === 0} onClick={() => showGroup(false)}>
                {t('iptv.group.hideAll')}
              </Button>
            </ListRow>
          </GroupedSection>
        )}

        {list && (q || state !== 'all') && rows.length > 0 && (
          <p className={styles.found} role="status">
            {t('iptv.table.found', { count: rows.length })}
          </p>
        )}
        {list && rows.length === 0 && (
          <div className={styles.empty}>
            <Icon name="search" size={40} strokeWidth={1.4} />
            <h2>{t('iptv.table.empty')}</h2>
            <p>{list.length ? t('iptv.table.emptyHint') : t('iptv.table.emptyNoChannels')}</p>
          </div>
        )}
        {!list && !error && <div className={styles.loading}>{t('iptv.loading')}</div>}

        {list && rows.length > 0 && (
          <div ref={tableRef} className={styles.table} data-logos={list.some((c) => c.logo) || undefined} role="table" aria-label={t('iptv.table.label')} aria-rowcount={rows.length + 1}>
            {!phone && (
              <div className={`${styles.tr} ${styles.thead}`} role="row" aria-rowindex={1} data-stuck={stuck || undefined}>
                <span role="columnheader" className={styles.cNum}>
                  {t('iptv.table.number')}
                </span>
                <span role="columnheader" className={styles.cLogo} aria-hidden />
                <span role="columnheader">{t('iptv.table.name')}</span>
                <span role="columnheader">{t('iptv.table.category')}</span>
                <span role="columnheader">{t('iptv.table.epg')}</span>
                <span role="columnheader">{t('iptv.table.why')}</span>
                <span role="columnheader" className={styles.cShown}>
                  {t('iptv.table.shown')}
                </span>
                <span role="columnheader" aria-hidden />
              </div>
            )}
            <VirtualList
              role="rowgroup"
              itemRole="row"
              count={rows.length}
              itemHeight={phone ? ROW_PHONE : ROW_WIDE}
              itemKey={(i) => rows[i]!.channelId}
              className={styles.tbody}
              itemClassName={styles.vrow}
              renderItem={(i) => (
                <ChannelRow
                  c={rows[i]!}
                  phone={phone}
                  editing={editing === rows[i]!.channelId}
                  onEdit={() => setEditing(rows[i]!.channelId)}
                  onRename={(n) => rename(rows[i]!, n)}
                  onShow={(on) => show(rows[i]!, on)}
                  onOpen={() => setSheet(rows[i]!)}
                  onReset={() => reset(rows[i]!)}
                />
              )}
            />
          </div>
        )}
      </div>

      {sheet && (
        <ChannelSheet
          c={sheet}
          onClose={() => setSheet(null)}
          onSaved={(msg) => {
            setSheet(null)
            if (msg) setToast(msg)
            loadChannels()
          }}
        />
      )}
      {settings && source && (
        <SourceEditor
          source={source}
          xtreamEnabled={xtream}
          users={users}
          drop={dropNotice}
          onClose={() => setSettings(false)}
          onSave={async (body) => {
            const s = await updateSource(id, body)
            setSource(s)
            setSettings(false)
            setToast(t('iptv.editor.saved'))
            loadChannels()
          }}
        />
      )}
      {removing && source && (
        <Sheet
          title={t('iptv.sourceDeleteTitle', { name: source.name })}
          message={t('iptv.sourceDeleteMessage')}
          icon={<Icon name="trash" size={34} strokeWidth={1.6} />}
          onCancel={() => setRemoving(false)}
          actions={[
            { label: t('common.cancel'), kind: 'cancel', run: () => setRemoving(false) },
            {
              label: t('iptv.sourceDelete'),
              kind: 'destructive',
              run: () => {
                setRemoving(false)
                deleteSource(id).then(() => navigate('/tv/manage/sources', { replace: true }), failed)
              },
            },
          ]}
        />
      )}
      {toast && <Toast>{toast}</Toast>}
    </div>
  )
}

function ChannelRow({
  c,
  phone,
  editing,
  onEdit,
  onRename,
  onShow,
  onOpen,
  onReset,
}: {
  c: SourceChannel
  phone: boolean
  editing: boolean
  onEdit: () => void
  onRename: (n: string) => void
  onShow: (on: boolean) => void
  onOpen: () => void
  onReset: () => void
}) {
  const t = useT()
  const epg = epgLabel(t, c)
  const why = whyLabel(t, c)
  const badges = (
    <>
      {c.new && (
        <Badge tone="accent" round>
          {t('iptv.table.new')}
        </Badge>
      )}
      {!c.present && <Badge tone="warning">{t('iptv.table.gone')}</Badge>}
    </>
  )
  const rowClass = [styles.tr, phone ? styles.phoneRow : '', c.shown ? '' : styles.off].filter(Boolean).join(' ')
  if (phone)
    return (
      <div className={rowClass}>
        <span role="cell" className={styles.cNum}>
          {c.number}
        </span>
        <span role="cell" className={styles.cLogo}>
          <ChannelLogo url={c.logo || undefined} size="xs" reserve />
        </span>
        <span role="cell" className={styles.cMain}>
          <button type="button" className={styles.rowButton} onClick={onOpen} aria-label={t('iptv.table.open', { name: c.name })}>
            <span className={styles.nameLine}>
              <span className={styles.cut}>{c.name}</span>
              {badges}
            </span>
            <span className={styles.line2}>{[c.category, epg, c.why === 'source-default' ? '' : why].filter(Boolean).join(' · ')}</span>
          </button>
        </span>
        <span role="cell" className={styles.cShown}>
          <Toggle label={t('iptv.table.show', { name: c.name })} checked={c.shown} onChange={onShow} />
        </span>
      </div>
    )
  return (
    <div className={rowClass}>
      <span role="cell" className={styles.cNum}>
        {c.number}
      </span>
      <span role="cell" className={styles.cLogo}>
        <ChannelLogo url={c.logo || undefined} size="xs" reserve />
      </span>
      <span role="cell" className={styles.cName}>
        {editing ? (
          <NameField c={c} onDone={onRename} />
        ) : (
          <span className={styles.nameLine}>
            <button type="button" className={styles.nameButton} onClick={onEdit} aria-label={t('iptv.table.rename', { name: c.name })}>
              {c.name}
            </button>
            {badges}
          </span>
        )}
      </span>
      <span role="cell" className={styles.cText}>
        {c.category}
      </span>
      <span role="cell" className={styles.cEpg}>
        <button type="button" className={styles.cellButton} onClick={onOpen} aria-label={`${t('iptv.table.epg')}: ${epg}`}>
          {c.epg.how ? (
            <>
              <span className={styles.cut}>{c.epg.xmlId}</span>
              <Badge tone={c.epg.manual ? 'accent' : 'default'}>{epg}</Badge>
            </>
          ) : (
            <span className={styles.none}>{epg}</span>
          )}
        </button>
      </span>
      <span role="cell" className={c.why === 'source-default' ? `${styles.cWhy} ${styles.whyDefault}` : styles.cWhy}>
        {why}
      </span>
      <span role="cell" className={styles.cShown}>
        <Toggle label={t('iptv.table.show', { name: c.name })} checked={c.shown} onChange={onShow} size="small" />
      </span>
      <span role="cell" className={styles.cMore}>
        <Menu label={t('iptv.table.more', { name: c.name })} placement="bottom-end" trigger={(p) => <IconButton {...p} size="xs" variant="plain" icon="ellipsis" label={t('iptv.table.more', { name: c.name })} />}>
          {(close) => (
            <>
              <MenuItem icon={<Icon name="pencil" size={16} />} label={t('iptv.table.editItem')} onSelect={() => (close(false), onOpen())} />
              <MenuItem icon={<Icon name="textSize" size={16} />} label={t('iptv.table.renameItem')} onSelect={() => (close(false), onEdit())} />
              <MenuSeparator />
              <MenuItem icon={<Icon name="restart" size={16} />} label={t('iptv.table.reset')} detail={t('iptv.table.resetDetail')} onSelect={() => (close(), onReset())} />
            </>
          )}
        </Menu>
      </span>
    </div>
  )
}

function NameField({ c, onDone }: { c: SourceChannel; onDone: (n: string) => void }) {
  const t = useT()
  const [v, setV] = useState(c.name)
  const done = useRef(false)
  const finish = (n: string) => {
    if (done.current) return
    done.current = true
    onDone(n)
  }
  return (
    <TextField
      size="small"
      label={t('iptv.table.nameField')}
      hideLabel
      value={v}
      placeholder={c.providerName}
      autoFocus
      className={styles.nameField}
      onChange={(e) => setV(e.target.value)}
      onBlur={() => finish(v)}
      onKeyDown={(e) => {
        if (e.key === 'Enter') finish(v)
        if (e.key === 'Escape') finish(c.name)
      }}
    />
  )
}
