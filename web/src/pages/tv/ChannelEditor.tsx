// The channel editor: name, group and number, its logo, who sees the
// channel (an admin's shared or personal), whose library it plays, what it
// shows (a selection by kind, genre and decade, or its own ordered list —
// «Состав канала»: movies, series, episodes, folders), the order, the age
// limit and whether it is on.
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
  type ChannelPick,
  type ChannelRules,
  uploadChannelLogo,
} from '../../api/channels'
import { useT } from '../../i18n'
import { GroupedSection, ListRow } from '../../ui/List'
import { Select } from '../../ui/Select'
import { NumberField } from '../../ui/NumberField'
import { SegmentedControl } from '../../ui/SegmentedControl'
import { Sheet } from '../../ui/Sheet'
import { TextField } from '../../ui/TextField'
import { Toggle } from '../../ui/Toggle'
import { ChannelExtrasRow } from './ChannelExtrasRow'
import { ChannelLogoRow } from './ChannelLogoRow'
import { ChannelPicks, picksOf } from './ChannelPicks'
import { ChannelPrompt } from './ChannelPrompt'
import { ChannelTags, tagRulesOf, type TagRules } from './ChannelTags'
import styles from './Tv.module.css'

const RATINGS = ['0+', '6+', '12+', '16+', '18+']
type Content = 'rules' | 'picks'

export interface ChannelEditorProps {
  /** null: a new channel. */
  channel: Channel | null
  me: { id: number; isAdmin: boolean }
  onClose: () => void
  /** Saved; `warning`: the channel is saved but its new logo was not taken. */
  onSaved: (c: Channel, warning?: string) => void
  /** A saved channel's logo was uploaded or removed (the list shows it). */
  onLogoChanged?: () => void
}

export function ChannelEditor({ channel, me, onClose, onSaved, onLogoChanged }: ChannelEditorProps) {
  const t = useT()
  const r: ChannelRules = channel?.rules ?? {}
  const [name, setName] = useState(channel?.name ?? '')
  // the name «Опишите канал» gave (replaced by the next one until edited)
  const [autoName, setAutoName] = useState('')
  const [category, setCategory] = useState(channel?.category ?? '')
  const [number, setNumber] = useState(channel?.number ?? 0)
  const [personal, setPersonal] = useState(channel ? channel.personal : !me.isAdmin)
  const [enabled, setEnabled] = useState(channel?.enabled ?? true)
  const [picks, setPicks] = useState<ChannelPick[]>(() => picksOf(r))
  const [content, setContent] = useState<Content>(picks.length ? 'picks' : 'rules')
  const [tags, setTags] = useState<TagRules>(() => tagRulesOf(r))
  const [library, setLibrary] = useState(String(r.libraryUser ?? 0))
  const [order, setOrder] = useState<ChannelOrder>(r.order ?? 'shuffle')
  const [rating, setRating] = useState(r.maxRating ?? '')
  const [extras, setExtras] = useState(!!r.extras)
  const [logoUrl, setLogoUrl] = useState(channel?.logoUrl)
  const [pendingLogo, setPendingLogo] = useState<File | null>(null)
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

  const needPicks = content === 'picks' && picks.length === 0
  const save = async () => {
    const rules: ChannelRules = { order, maxRating: rating || undefined, libraryUser: Number(libraryValue) || undefined }
    // round 1's titles are rewritten as picks of kind «title»
    if (content === 'picks') rules.picks = picks
    else {
      Object.assign(rules, tags)
      rules.extras = extras || undefined
    }
    const body: ChannelInput = { name, number: number || undefined, category, personal, enabled, rules }
    setBusy(true)
    setError('')
    try {
      const saved = channel ? await updateChannel(channel.id, body) : await createChannel(body)
      // a new channel's logo goes up once the channel exists
      let warning: string | undefined
      if (!channel && pendingLogo) {
        try {
          await uploadChannelLogo(saved.id, pendingLogo)
        } catch (e) {
          warning = t('tv.logo.notSaved', { error: errorText(e, t('tv.logo.failed')) })
        }
      }
      onSaved(saved, warning)
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
        { label: channel ? t('tv.editor.save') : t('tv.editor.create'), kind: 'primary', disabled: !name.trim() || needPicks || busy, run: () => void save() },
      ]}
    >
      {/* first what it shows: a sentence, or the tags; then the channel itself */}
      {content === 'rules' && (
        <ChannelPrompt
          onSuggested={(sg) => {
            setTags(tagRulesOf(sg.rules))
            if (!name.trim() || name === autoName) {
              setName(sg.name)
              setAutoName(sg.name)
            }
          }}
        />
      )}
      <GroupedSection>
        <ListRow label={t('tv.editor.content')} stack>
          <SegmentedControl
            label={t('tv.editor.content')}
            size="small"
            value={content}
            onChange={setContent}
            segments={[
              { value: 'rules', label: t('tv.editor.byRules') },
              { value: 'picks', label: t('tv.editor.byTitles') },
            ]}
          />
        </ListRow>
      </GroupedSection>
      {content === 'rules' ? (
        <ChannelTags options={options} value={tags} onChange={setTags} />
      ) : (
        <ChannelPicks picks={picks} onChange={setPicks} options={options} info={channel?.pickInfo} need={needPicks} />
      )}

      <GroupedSection title={t('tv.editor.channel')}>
        <li className={styles.formRow}>
          <div className={styles.form}>
            <TextField label={t('tv.editor.name')} value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" maxLength={80} />
            <TextField label={t('tv.editor.category')} hint={t('tv.editor.categoryHint')} value={category} onChange={(e) => setCategory(e.target.value)} autoComplete="off" maxLength={80} />
          </div>
        </li>
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
        <ChannelLogoRow
          channelId={channel?.id ?? null}
          url={logoUrl}
          onChanged={(u) => {
            setLogoUrl(u)
            onLogoChanged?.()
          }}
          pending={pendingLogo}
          onPending={setPendingLogo}
        />
        <ListRow label={t('tv.enabled')}>
          <Toggle label={t('tv.enabled')} checked={enabled} onChange={setEnabled} />
        </ListRow>
      </GroupedSection>

      <GroupedSection title={t('tv.editor.playback')}>
        <ListRow label={t('tv.editor.library')}>
          <Select label={t('tv.editor.library')} value={libraryValue} options={libraryOptions} onChange={setLibrary} placement="bottom-end" />
        </ListRow>
        {content === 'rules' && options?.extrasAvailable && <ChannelExtrasRow checked={extras} onChange={setExtras} />}
        <ListRow
          label={t('tv.editor.order')}
          hint={content === 'picks' ? (order === 'ordered' ? t('tv.picks.orderedHint') : t('tv.picks.shuffleHint')) : t('tv.editor.orderHint')}
          stack
        >
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

      {error && (
        <p className={styles.formError} role="alert">
          {error}
        </p>
      )}
    </Sheet>
  )
}
