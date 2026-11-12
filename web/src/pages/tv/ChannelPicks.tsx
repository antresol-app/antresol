// «Состав канала» in the channel editor: the channel's own ordered list
// (rules.picks) — movies, whole series, single episodes and library folders —
// and the two ways to add to it: a search (movies, series, episodes) and a
// folder browser. The list reorders by drag and drop, by the keyboard and by
// «Выше» / «Ниже» (ui/SortableList). What a pick is called comes from the
// editor's options (titles), from the search hit or folder it was added
// from, or, for an episode saved earlier, from the library.
import { useEffect, useMemo, useState } from 'react'
import { errorText, getItem } from '../../api/client'
import { getChannelFolders, searchChannelOptions, type ChannelFolderLevel, type ChannelOptions, type ChannelPick, type ChannelPickInfo, type ChannelSearchHit } from '../../api/channels'
import { Icon, type IconName } from '../../components/Icon'
import { useT } from '../../i18n'
import { IconButton } from '../../ui/IconButton'
import { GroupedSection, ListRow } from '../../ui/List'
import { SegmentedControl } from '../../ui/SegmentedControl'
import { SortableList, SortableRowText } from '../../ui/SortableList'
import { TextField } from '../../ui/TextField'
import styles from './Tv.module.css'

/** A pick's stable key in the list. */
export const pickKey = (p: ChannelPick) => (p.kind === 'folder' ? `folder:${p.path}` : `${p.kind}:${p.id}`)

/** Round 1's chosen titles as picks (the editor saves them as picks). */
export function picksOf(r: { picks?: ChannelPick[]; titles?: number[] } | undefined): ChannelPick[] {
  if (r?.picks?.length) return r.picks
  return (r?.titles ?? []).map((id) => ({ kind: 'title' as const, id }))
}

/** A search hit as a pick: a movie or a series is a title, an episode one item. */
export const hitPick = (h: ChannelSearchHit): ChannelPick => h.pick ?? (h.kind === 'episode' ? { kind: 'item', id: h.id } : { kind: 'title', id: h.id })

interface Shown {
  icon: IconName
  title: string
  detail: string
  missing?: boolean
}

const lastSegment = (path: string) => path.split('/').filter(Boolean).pop() ?? path

export interface ChannelPicksProps {
  picks: ChannelPick[]
  onChange: (picks: ChannelPick[]) => void
  options: ChannelOptions | null
  /** The server's description of the saved picks (Channel.pickInfo). */
  info?: ChannelPickInfo[]
  /** Nothing chosen yet and the editor wants something. */
  need: boolean
}

export function ChannelPicks({ picks, onChange, options, info, need }: ChannelPicksProps) {
  const t = useT()
  // what the picks added in this editor (and the episodes read from the library) are called
  const kindWord = (k: 'movie' | 'series' | 'episode' | 'folder' | 'file') => t(`tv.picks.kind.${k}`)
  // the saved picks as the server describes them
  const [known, setKnown] = useState<Record<string, Shown>>(() => {
    const m: Record<string, Shown> = {}
    for (const x of info ?? []) {
      const p: ChannelPick = x.kind === 'folder' ? { kind: 'folder', path: x.path } : { kind: x.kind, id: x.id }
      const episode = x.kind === 'item' && !!x.subtitle
      const word = x.kind === 'folder' ? kindWord('folder') : episode ? kindWord('episode') : ''
      m[pickKey(p)] = {
        icon: x.kind === 'folder' ? 'folder' : episode ? 'tv' : 'film',
        title: x.title,
        detail: [x.missing ? t('tv.picks.missing') : word, x.kind === 'folder' ? x.subtitle ?? x.path : x.subtitle, x.year].filter(Boolean).join(' · '),
        missing: x.missing,
      }
    }
    return m
  })
  const remember = (k: string, s: Shown) => setKnown((m) => (m[k] ? m : { ...m, [k]: s }))

  const shownOf = (p: ChannelPick): Shown => {
    const k = pickKey(p)
    // a movie or a series of the options reads richer (its kind, episodes) unless it is missing
    const titled = p.kind === 'title' && options?.titles.some((o) => o.id === p.id)
    if (known[k] && !(titled && !known[k].missing)) return known[k]
    if (p.kind === 'folder') return { icon: 'folder', title: lastSegment(p.path), detail: [kindWord('folder'), p.path].join(' · ') }
    const x = options?.titles.find((o) => o.id === p.id)
    if (x) {
      const detail = [kindWord(x.kind), x.year, x.episodes ? t('tv.editor.episodes', { count: x.episodes }) : ''].filter(Boolean).join(' · ')
      return { icon: x.kind === 'series' ? 'tv' : 'film', title: x.title, detail }
    }
    return { icon: p.kind === 'item' ? 'tv' : 'film', title: options ? t('tv.picks.unknown') : t('tv.picks.loading'), detail: '' }
  }

  // an episode (or a title not among the options) saved earlier: its name from the library
  const missing = useMemo(
    () => (options ? picks.filter((p) => p.kind !== 'folder' && !known[pickKey(p)] && !options.titles.some((o) => o.id === p.id)) : []),
    [picks, known, options],
  )
  useEffect(() => {
    if (!missing.length) return
    const ac = new AbortController()
    for (const p of missing.slice(0, 50)) {
      if (p.kind === 'folder') continue
      getItem(p.id, ac.signal).then(
        (it) => {
          const episode = it.kind === 'episode'
          remember(pickKey(p), {
            icon: episode ? 'tv' : 'film',
            title: it.title,
            detail: [episode ? kindWord('episode') : kindWord(it.kind === 'series' ? 'series' : 'movie'), it.subtitle || it.year].filter(Boolean).join(' · '),
          })
        },
        () => undefined,
      )
    }
    return () => ac.abort()
  }, [missing.map(pickKey).join('|')])

  const has = useMemo(() => new Set(picks.map(pickKey)), [picks])
  const add = (p: ChannelPick, s: Shown) => {
    const k = pickKey(p)
    if (has.has(k)) return
    remember(k, s)
    onChange([...picks, p])
  }

  // ---------- adding ----------
  const [mode, setMode] = useState<'search' | 'folders'>('search')

  return (
    <>
      <GroupedSection
        title={
          <>
            {t('tv.picks.title')} · <span className={styles.titleCount}>{t('tv.picks.count', { count: picks.length })}</span>
          </>
        }
        footer={need ? t('tv.picks.need') : undefined}
      >
        <ListRow label={t('tv.picks.listLabel')} hint={t('tv.picks.listHint')} wide>
          <SortableList
            label={t('tv.picks.title')}
            items={picks}
            getKey={pickKey}
            itemLabel={(p) => shownOf(p).title}
            renderItem={(p) => {
              const s = shownOf(p)
              return <SortableRowText leading={<Icon name={s.icon} size={16} />} title={s.title} detail={s.detail || undefined} />
            }}
            onChange={onChange}
            removable
            moveButtons
            empty={<p className={styles.picksEmpty}>{t('tv.picks.empty')}</p>}
          />
        </ListRow>
      </GroupedSection>

      <GroupedSection title={t('tv.picks.add')}>
        <ListRow
          label={
            <SegmentedControl
              label={t('tv.picks.addFrom')}
              size="small"
              value={mode}
              onChange={setMode}
              segments={[
                { value: 'search', label: t('tv.picks.search') },
                { value: 'folders', label: t('tv.picks.folders') },
              ]}
            />
          }
        />
        {mode === 'search' ? <SearchRows has={has} onAdd={add} kindWord={kindWord} /> : <FolderRows has={has} onAdd={add} kindWord={kindWord} />}
      </GroupedSection>
    </>
  )
}

type AddProps = { has: Set<string>; onAdd: (p: ChannelPick, s: Shown) => void; kindWord: (k: 'movie' | 'series' | 'episode' | 'folder' | 'file') => string }

/** The trailing «+» of a row that adds; a check once it is in the list. */
function AddButton({ name, added, onAdd }: { name: string; added: boolean; onAdd: () => void }) {
  const t = useT()
  return (
    <IconButton
      variant="plain"
      size="sm"
      icon={added ? 'checkCircle' : 'plusCircle'}
      className={added ? styles.pickAdded : styles.pickAdd}
      label={added ? t('tv.picks.added', { name }) : t('tv.picks.addOne', { name })}
      disabled={added}
      onClick={onAdd}
    />
  )
}

function Glyph({ icon }: { icon: IconName }) {
  return (
    <span className={styles.pickGlyph} aria-hidden>
      <Icon name={icon} size={16} />
    </span>
  )
}

function SearchRows({ has, onAdd, kindWord }: AddProps) {
  const t = useT()
  const [q, setQ] = useState('')
  const [hits, setHits] = useState<ChannelSearchHit[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
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
      searchChannelOptions(query, ac.signal).then(
        (r) => {
          setHits(r.slice(0, 60))
          setError('')
          setBusy(false)
        },
        (e: unknown) => {
          if (ac.signal.aborted) return
          setError(errorText(e, t('common.loadFailed')))
          setBusy(false)
        },
      )
    }, 250)
    return () => {
      window.clearTimeout(id)
      ac.abort()
    }
  }, [q, t])
  return (
    <>
      <ListRow
        label={
          <div className={styles.pickField}>
            <TextField
              type="search"
              size="small"
              label={t('tv.picks.searchField')}
              hideLabel
              placeholder={t('tv.picks.searchField')}
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
        const p = hitPick(h)
        const s: Shown = { icon: h.kind === 'movie' ? 'film' : 'tv', title: h.title, detail: [kindWord(h.kind), h.subtitle, h.year].filter(Boolean).join(' · ') }
        return (
          <ListRow key={`${h.kind}:${h.id}`} leading={<Glyph icon={s.icon} />} label={<span className={styles.pickName}>{h.title}</span>} hint={s.detail}>
            <AddButton name={h.title} added={has.has(pickKey(p))} onAdd={() => onAdd(p, s)} />
          </ListRow>
        )
      })}
    </>
  )
}

function FolderRows({ has, onAdd, kindWord }: AddProps) {
  const t = useT()
  const [path, setPath] = useState('')
  const [level, setLevel] = useState<ChannelFolderLevel | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    const ac = new AbortController()
    setLevel(null)
    setError('')
    getChannelFolders(path, ac.signal).then(setLevel, (e: unknown) => !ac.signal.aborted && setError(errorText(e, t('common.loadFailed'))))
    return () => ac.abort()
  }, [path, t])
  const parts = path.split('/').filter(Boolean)
  const up = parts.slice(0, -1).join('/')
  return (
    <>
      <ListRow
        leading={path ? <IconButton variant="plain" size="xs" icon="chevronLeft" label={t('tv.picks.back')} onClick={() => setPath(up)} /> : <Glyph icon="folder" />}
        label={<span className={styles.pickPath}>{[t('tv.picks.root'), ...parts].join(' / ')}</span>}
      >
        {path && <AddButton name={lastSegment(path)} added={has.has(`folder:${path}`)} onAdd={() => onAdd({ kind: 'folder', path }, { icon: 'folder', title: lastSegment(path), detail: [kindWord('folder'), path].join(' · ') })} />}
      </ListRow>
      {error && <ListRow label={<span className={styles.formError}>{error}</span>} />}
      {!error && !level && <ListRow label={<span className={styles.pickNote}>{t('tv.picks.loading')}</span>} />}
      {level && level.folders.length + level.files.length === 0 && <ListRow label={<span className={styles.pickNote}>{t('tv.picks.emptyFolder')}</span>} />}
      {level?.folders.map((f) => (
        <ListRow key={f.path} leading={<Glyph icon="folder" />} label={<span className={styles.pickName}>{f.name}</span>}>
          <AddButton name={f.name} added={has.has(`folder:${f.path}`)} onAdd={() => onAdd({ kind: 'folder', path: f.path }, { icon: 'folder', title: f.name, detail: [kindWord('folder'), f.path].join(' · ') })} />
          <IconButton variant="plain" size="sm" icon="chevronRight" label={t('tv.picks.open', { name: f.name })} onClick={() => setPath(f.path)} />
        </ListRow>
      ))}
      {level?.files.map((f) => (
        <ListRow key={f.id} leading={<Glyph icon={f.kind === 'episode' ? 'tv' : 'film'} />} label={<span className={styles.pickName}>{f.name}</span>}>
          <AddButton name={f.name} added={has.has(`item:${f.id}`)} onAdd={() => onAdd({ kind: 'item', id: f.id }, { icon: f.kind === 'episode' ? 'tv' : 'film', title: f.name, detail: [kindWord('file'), path].filter(Boolean).join(' · ') })} />
        </ListRow>
      ))}
    </>
  )
}
