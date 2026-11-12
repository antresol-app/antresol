// The channel editor: name, group and number, who sees the channel (an
// admin's shared or personal), whose library it plays, what it shows (a
// selection by kind, genre and decade, or chosen titles), the order, the
// age limit and whether it is on.
import { useEffect, useMemo, useState } from 'react'
import { errorText } from '../../api/client'
import {
  createChannel,
  getChannelOptions,
  updateChannel,
  type Channel,
  type ChannelInput,
  type ChannelOptions,
  type ChannelOrder,
  type ChannelRules,
} from '../../api/channels'
import { useT } from '../../i18n'
import { GroupedSection, ListRow } from '../../ui/List'
import { MultiSelect, Select } from '../../ui/Select'
import { NumberField } from '../../ui/NumberField'
import { SegmentedControl } from '../../ui/SegmentedControl'
import { Sheet } from '../../ui/Sheet'
import { TextField } from '../../ui/TextField'
import { Toggle } from '../../ui/Toggle'
import { ChannelExtrasRow } from './ChannelExtrasRow'
import styles from './Tv.module.css'

const RATINGS = ['0+', '6+', '12+', '16+', '18+']
type Kind = 'movie' | 'series'
type Content = 'rules' | 'titles'

export interface ChannelEditorProps {
  /** null: a new channel. */
  channel: Channel | null
  me: { id: number; isAdmin: boolean }
  onClose: () => void
  onSaved: (c: Channel) => void
}

export function ChannelEditor({ channel, me, onClose, onSaved }: ChannelEditorProps) {
  const t = useT()
  const r: ChannelRules = channel?.rules ?? {}
  const [name, setName] = useState(channel?.name ?? '')
  const [category, setCategory] = useState(channel?.category ?? '')
  const [number, setNumber] = useState(channel?.number ?? 0)
  const [personal, setPersonal] = useState(channel ? channel.personal : !me.isAdmin)
  const [enabled, setEnabled] = useState(channel?.enabled ?? true)
  const [content, setContent] = useState<Content>(r.titles?.length ? 'titles' : 'rules')
  const [kinds, setKinds] = useState<Kind[]>(r.kinds?.length ? r.kinds : ['movie', 'series'])
  const [genres, setGenres] = useState<string[]>(r.genres ?? [])
  const [decade, setDecade] = useState(r.yearFrom ? String(r.yearFrom) : '')
  const [titles, setTitles] = useState<number[]>(r.titles ?? [])
  const [library, setLibrary] = useState(String(r.libraryUser ?? 0))
  const [order, setOrder] = useState<ChannelOrder>(r.order ?? 'shuffle')
  const [rating, setRating] = useState(r.maxRating ?? '')
  const [extras, setExtras] = useState(!!r.extras)
  const [query, setQuery] = useState('')
  const [options, setOptions] = useState<ChannelOptions | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let live = true
    getChannelOptions().then(
      (o) => live && setOptions(o),
      (e: unknown) => live && setError(errorText(e, t('common.loadFailed'))),
    )
    return () => {
      live = false
    }
  }, [t])

  // whose library: the whole one, the profile's own, or (an admin's shared channel) any profile's
  const libraryOptions = useMemo(() => {
    const list = [
      { value: '0', label: t('tv.rules.all') },
      { value: String(me.id), label: t('tv.rules.mine') },
    ]
    if (me.isAdmin && !personal) {
      for (const p of options?.profiles ?? []) if (p.id !== me.id) list.push({ value: String(p.id), label: t('tv.rules.of', { name: p.name }) })
    }
    return list
  }, [options, me, personal, t])
  const libraryValue = libraryOptions.some((o) => o.value === library) ? library : '0'

  const chosen = useMemo(() => new Set(titles), [titles])
  const shown = useMemo(() => {
    const all = options?.titles ?? []
    const q = query.trim().toLowerCase()
    const hit = q ? all.filter((x) => x.title.toLowerCase().includes(q)) : all
    // the chosen ones first, in the order they were chosen (the order they play in)
    const picked = titles.map((id) => all.find((x) => x.id === id)).filter((x): x is NonNullable<typeof x> => !!x && (!q || hit.includes(x)))
    return [...picked, ...hit.filter((x) => !chosen.has(x.id))].slice(0, 200)
  }, [options, query, titles, chosen])

  const needTitles = content === 'titles' && titles.length === 0
  const save = async () => {
    const rules: ChannelRules = { order, maxRating: rating || undefined, libraryUser: Number(libraryValue) || undefined }
    if (content === 'titles') rules.titles = titles
    else {
      rules.kinds = kinds.length === 2 ? undefined : kinds
      rules.genres = genres.length ? genres : undefined
      rules.extras = extras || undefined
      if (decade) {
        rules.yearFrom = Number(decade)
        rules.yearTo = Number(decade) + 9
      }
    }
    const body: ChannelInput = { name, number: number || undefined, category, personal, enabled, rules }
    setBusy(true)
    setError('')
    try {
      onSaved(channel ? await updateChannel(channel.id, body) : await createChannel(body))
    } catch (e) {
      setError(errorText(e, t('common.genericError')))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Sheet
      title={channel ? t('tv.editor.edit', { name: channel.name }) : t('tv.editor.new')}
      role="dialog"
      size="large"
      busy={busy}
      onCancel={onClose}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onClose },
        { label: channel ? t('tv.editor.save') : t('tv.editor.create'), kind: 'primary', disabled: !name.trim() || needTitles || busy, run: () => void save() },
      ]}
    >
      <div className={styles.form}>
        <TextField label={t('tv.editor.name')} value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" maxLength={80} />
        <TextField label={t('tv.editor.category')} hint={t('tv.editor.categoryHint')} value={category} onChange={(e) => setCategory(e.target.value)} autoComplete="off" maxLength={80} />
      </div>
      <GroupedSection>
        {channel && (
          <ListRow label={t('tv.editor.number')}>
            <NumberField label={t('tv.editor.number')} value={number} onChange={setNumber} min={1} max={99999} chars={3} />
          </ListRow>
        )}
        {me.isAdmin && (!channel || !channel.personal || channel.canEdit) && (
          <ListRow label={t('tv.editor.who')} stack>
            <SegmentedControl
              label={t('tv.editor.who')}
              size="small"
              value={personal ? 'me' : 'all'}
              onChange={(v) => setPersonal(v === 'me')}
              segments={[
                { value: 'all', label: t('tv.editor.everyone') },
                { value: 'me', label: t('tv.editor.onlyMe') },
              ]}
            />
          </ListRow>
        )}
        <ListRow label={t('tv.enabled')}>
          <Toggle label={t('tv.enabled')} checked={enabled} onChange={setEnabled} />
        </ListRow>
      </GroupedSection>

      <GroupedSection title={t('tv.editor.content')}>
        <ListRow label={t('tv.editor.library')}>
          <Select label={t('tv.editor.library')} value={libraryValue} options={libraryOptions} onChange={setLibrary} placement="bottom-end" />
        </ListRow>
        <ListRow label={t('tv.editor.content')} stack>
          <SegmentedControl
            label={t('tv.editor.content')}
            size="small"
            value={content}
            onChange={setContent}
            segments={[
              { value: 'rules', label: t('tv.editor.byRules') },
              { value: 'titles', label: t('tv.editor.byTitles') },
            ]}
          />
        </ListRow>
        {content === 'rules' && (
          <>
            <ListRow label={t('tv.editor.kinds')}>
              <MultiSelect
                label={t('tv.editor.kinds')}
                value={kinds}
                min={1}
                options={[
                  { value: 'movie' as Kind, label: t('tv.rules.movies') },
                  { value: 'series' as Kind, label: t('tv.rules.series') },
                ]}
                onChange={setKinds}
                placement="bottom-end"
              />
            </ListRow>
            <ListRow label={t('tv.editor.genres')}>
              <MultiSelect
                label={t('tv.editor.genres')}
                value={genres}
                none={t('tv.editor.anyGenre')}
                options={(options?.genres ?? []).map((g) => ({ value: g.key, label: g.name }))}
                onChange={setGenres}
                placement="bottom-end"
              />
            </ListRow>
            <ListRow label={t('tv.editor.years')}>
              <Select
                label={t('tv.editor.years')}
                value={decade}
                options={[{ value: '', label: t('tv.editor.anyYears') }, ...(options?.decades ?? []).map((d) => ({ value: String(d), label: t('tv.rules.decade', { decade: d }) }))]}
                onChange={setDecade}
                placement="bottom-end"
              />
            </ListRow>
            {options?.extrasAvailable && <ChannelExtrasRow checked={extras} onChange={setExtras} />}
          </>
        )}
        <ListRow label={t('tv.editor.order')} hint={t('tv.editor.orderHint')} stack>
          <SegmentedControl
            label={t('tv.editor.order')}
            size="small"
            value={order}
            onChange={setOrder}
            segments={[
              { value: 'shuffle', label: t('tv.rules.shuffle') },
              { value: 'ordered', label: t('tv.rules.ordered') },
            ]}
          />
        </ListRow>
        <ListRow label={t('tv.editor.rating')} hint={t('tv.editor.ratingHint')}>
          <Select
            label={t('tv.editor.rating')}
            value={rating}
            options={[{ value: '', label: t('tv.editor.noRating') }, ...RATINGS.map((x) => ({ value: x, label: x }))]}
            onChange={setRating}
            placement="bottom-end"
          />
        </ListRow>
      </GroupedSection>

      {content === 'titles' && (
        <GroupedSection
          title={
            <>
              {t('tv.editor.byTitles')} · <span className={styles.titleCount}>{t('tv.rules.titlesCount', { count: titles.length })}</span>
            </>
          }
          footer={needTitles ? t('tv.editor.needTitles') : undefined}
        >
          <ListRow label={t('tv.editor.search')} wide>
            <TextField type="search" size="small" label={t('tv.editor.search')} hideLabel placeholder={t('tv.editor.search')} value={query} onChange={(e) => setQuery(e.target.value)} onClear={() => setQuery('')} />
          </ListRow>
          {options && shown.length === 0 && <ListRow label={t('tv.editor.noTitles')} />}
          {shown.map((x) => (
            <ListRow
              key={x.id}
              label={x.title}
              hint={[x.kind === 'series' ? t('tv.rules.series') : t('tv.rules.movies'), x.year, x.episodes ? t('tv.editor.episodes', { count: x.episodes }) : ''].filter(Boolean).join(' · ')}
            >
              <Toggle
                label={x.title}
                checked={chosen.has(x.id)}
                onChange={(on) => setTitles((cur) => (on ? [...cur, x.id] : cur.filter((id) => id !== x.id)))}
              />
            </ListRow>
          ))}
        </GroupedSection>
      )}
      {error && (
        <p className={styles.formError} role="alert">
          {error}
        </p>
      )}
    </Sheet>
  )
}
