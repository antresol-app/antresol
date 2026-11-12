// A set's sheet: its name, the sources it takes, and — a set being a
// subset of what is already visible — per source the categories it leaves
// out and the single channels it hides (a search over the visible
// channels of its sources; the hidden ones listed while the search is
// empty). Categories are matched by name, so hiding one hides it in every
// source of the set.
import { useEffect, useMemo, useState } from 'react'
import { errorText } from '../../../api/client'
import { createSet, getSourceChannels, updateSet, type ChannelSet, type Source, type SourceChannel } from '../../../api/iptv'
import { useT } from '../../../i18n'
import { GroupedSection, ListRow } from '../../../ui/List'
import { Sheet } from '../../../ui/Sheet'
import { TextField } from '../../../ui/TextField'
import { Toggle } from '../../../ui/Toggle'
import { num } from './SourcesPage'
import styles from './Sources.module.css'

const key = (s: string) => s.trim().toLocaleLowerCase()
const MAX_HITS = 30

export function SetSheet({ set, sources, onClose, onSaved }: { set: ChannelSet | null; sources: Source[]; onClose: () => void; onSaved: (s: ChannelSet) => void }) {
  const t = useT()
  const [name, setName] = useState(set?.name ?? '')
  const [chosen, setChosen] = useState<number[]>(set?.sources ?? sources.map((s) => s.id))
  const [hiddenGroups, setHiddenGroups] = useState<string[]>(set?.hiddenGroups ?? [])
  const [hiddenChannels, setHiddenChannels] = useState<number[]>(set?.hiddenChannels ?? [])
  const [channels, setChannels] = useState<Record<number, SourceChannel[]>>({})
  const [q, setQ] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  // the visible channels of every source the set takes (loaded once each)
  useEffect(() => {
    const ac = new AbortController()
    for (const id of chosen)
      if (!channels[id])
        getSourceChannels(id, ac.signal).then(
          (r) => setChannels((c) => ({ ...c, [id]: r.channels.filter((x) => x.shown && x.present) })),
          () => undefined,
        )
    return () => ac.abort()
  }, [chosen, channels])

  const hiddenGroup = useMemo(() => new Set(hiddenGroups.map(key)), [hiddenGroups])
  const isGroupHidden = (g: string) => hiddenGroup.has(key(g))
  const toggleGroup = (g: string, on: boolean) => setHiddenGroups((h) => (on ? h.filter((x) => key(x) !== key(g)) : [...h.filter((x) => key(x) !== key(g)), g]))
  const toggleChannel = (id: number, on: boolean) => setHiddenChannels((h) => (on ? h.filter((x) => x !== id) : [...h.filter((x) => x !== id), id]))

  const inSet = sources.filter((s) => chosen.includes(s.id))
  const categories = (id: number) => {
    const m = new Map<string, number>()
    for (const c of channels[id] ?? []) m.set(c.category, (m.get(c.category) ?? 0) + 1)
    return [...m]
  }
  const all = useMemo(() => inSet.flatMap((s) => (channels[s.id] ?? []).map((c) => ({ c, source: s.name }))), [inSet, channels])
  const needle = q.trim().toLocaleLowerCase()
  const hits = needle ? all.filter(({ c }) => !isGroupHidden(c.category) && (c.name.toLocaleLowerCase().includes(needle) || String(c.number) === needle)).slice(0, MAX_HITS) : []
  const hidden = all.filter(({ c }) => hiddenChannels.includes(c.channelId))

  const save = () => {
    const n = name.trim()
    if (!n) return
    setBusy(true)
    const body = { name: n, sources: sources.map((s) => s.id).filter((id) => chosen.includes(id)), hiddenGroups, hiddenChannels }
    ;(set ? updateSet(set.id, body) : createSet(body)).then(onSaved, (e: unknown) => {
      setBusy(false)
      setError(errorText(e, t('common.genericError')))
    })
  }

  const channelRow = ({ c, source }: { c: SourceChannel; source: string }) => (
    <ListRow key={c.channelId} label={`${c.number} · ${c.name}`} hint={`${c.category} · ${source}`}>
      <Toggle label={t('iptv.setChannelIn', { name: c.name })} checked={!hiddenChannels.includes(c.channelId)} onChange={(on) => toggleChannel(c.channelId, on)} />
    </ListRow>
  )

  return (
    <Sheet
      title={set ? t('iptv.setEditTitle') : t('iptv.setNewTitle')}
      role="dialog"
      size="large"
      busy={busy}
      onCancel={onClose}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onClose },
        { label: t('iptv.editor.save'), kind: 'primary', disabled: !name.trim(), run: save },
      ]}
    >
      <div className={styles.form}>
        <TextField label={t('iptv.setName')} placeholder={t('iptv.setNamePlaceholder')} value={name} onChange={(e) => setName(e.target.value)} autoFocus={!set} />
        <GroupedSection title={t('iptv.setSources')} footer={t('iptv.setSourcesFoot')}>
          {sources.map((s) => (
            <ListRow key={s.id} label={s.name}>
              <Toggle label={s.name} checked={chosen.includes(s.id)} onChange={(on) => setChosen((c) => (on ? [...c, s.id] : c.filter((x) => x !== s.id)))} />
            </ListRow>
          ))}
        </GroupedSection>

        {inSet.map((s) => (
          <GroupedSection key={s.id} title={t('iptv.setCategories', { name: s.name })} footer={t('iptv.setCategoriesFoot')}>
            {!channels[s.id] && <ListRow label={t('iptv.loading')} />}
            {channels[s.id] && categories(s.id).length === 0 && <ListRow label={t('iptv.setNoVisible')} />}
            {categories(s.id).map(([g, count]) => (
              <ListRow key={g} label={g || t('iptv.table.noGroup')} hint={t('iptv.setCategoryCount', { count: num(count) })}>
                <Toggle label={t('iptv.setCategoryIn', { name: g })} checked={!isGroupHidden(g)} onChange={(on) => toggleGroup(g, on)} />
              </ListRow>
            ))}
          </GroupedSection>
        ))}

        {inSet.length > 0 && (
          <GroupedSection title={t('iptv.setChannels')} footer={t('iptv.setChannelsFoot')}>
            <ListRow label={t('iptv.setChannelSearch')} wide>
              <TextField type="search" size="small" label={t('iptv.setChannelSearch')} hideLabel placeholder={t('iptv.setChannelSearchPlaceholder')} value={q} onChange={(e) => setQ(e.target.value)} onClear={() => setQ('')} className={styles.nameField} />
            </ListRow>
            {needle && hits.length === 0 && <ListRow label={t('iptv.table.empty')} />}
            {needle && hits.map(channelRow)}
            {!needle && hidden.length === 0 && <ListRow label={t('iptv.setNoHidden')} />}
            {!needle && hidden.map(channelRow)}
          </GroupedSection>
        )}
        {error && (
          <p className={styles.formError} role="alert">
            {error}
          </p>
        )}
      </div>
    </Sheet>
  )
}
