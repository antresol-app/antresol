// One external channel (a tap on a phone's row, «Изменить…» in a row's «⋯»):
// its name (empty: the playlist's), category (a picker over the categories,
// «Из плейлиста» or «Новая категория…»), number (0: automatic), the switch,
// its logo, and its guide — what it is bound to now, a search over the
// guides' channels to bind it by hand, «Отвязать» to give it back to the
// automatic match. Saving sends only what changed. «Удалить из источника…»
// (the page confirms) hides the channel for good. When the source merges
// variants: the channel's group of variants (best first), how it was made
// (the rules, the AI helper, the admin), «Отдельный канал» to take this
// entry out of it, «Объединить с…» to make it a variant of another channel
// of the playlist (a search over the list), and back to automatic.
import { useEffect, useState } from 'react'
import { ApiError, errorText } from '../../../api/client'
import { bindEPG, getCategories, patchChannels, searchEPG, setVariant, unbindEPG, type EPGHit, type SourceChannel } from '../../../api/iptv'
import { Icon } from '../../../components/Icon'
import { useT } from '../../../i18n'
import { Badge } from '../../../ui/Badge'
import { Button } from '../../../ui/Button'
import { IconButton } from '../../../ui/IconButton'
import { GroupedSection, ListLinkRow, ListRow } from '../../../ui/List'
import { NumberField } from '../../../ui/NumberField'
import { Select } from '../../../ui/Select'
import { Sheet } from '../../../ui/Sheet'
import { TextField } from '../../../ui/TextField'
import { Toggle } from '../../../ui/Toggle'
import { ChannelLogoRow } from '../ChannelLogoRow'
import { epgLabel } from './labels'
import styles from './Sources.module.css'

const PLAYLIST = '\u0000playlist'
const NEW = '\u0000new'

export function ChannelSheet({
  c,
  sourceId,
  all,
  variantsOn,
  onClose,
  onSaved,
  onRemove,
}: {
  c: SourceChannel
  sourceId: number
  /** The source's channels (the merge search). */
  all: SourceChannel[]
  variantsOn: boolean
  onClose: () => void
  onSaved: (message?: string) => void
  /** «Удалить из источника…»: the page asks, then hides the channel for good. */
  onRemove: (c: SourceChannel) => void
}) {
  const t = useT()
  const [name, setName] = useState(c.name === c.providerName ? '' : c.name)
  const [category, setCategory] = useState(c.category === c.group ? PLAYLIST : c.category)
  const [newCategory, setNewCategory] = useState('')
  const [categories, setCategories] = useState<string[]>([])
  const [number, setNumber] = useState(c.numberManual ? c.number : 0)
  const [shown, setShown] = useState(c.shown)
  const [logo, setLogo] = useState<string | undefined>(c.logo || undefined)
  const [pending, setPending] = useState<File | null>(null)
  const [binding, setBinding] = useState(false)
  const [merging, setMerging] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [numberError, setNumberError] = useState('')

  useEffect(() => {
    getCategories().then(setCategories, () => undefined)
  }, [])

  const options = [
    { value: PLAYLIST, label: t('iptv.sheet.categoryPlaylist', { group: c.group || t('iptv.table.noGroup') }) },
    ...categories.filter((x) => x !== c.group).map((x) => ({ value: x, label: x })),
    ...(category !== PLAYLIST && category !== NEW && !categories.includes(category) ? [{ value: category, label: category }] : []),
    { value: NEW, label: t('iptv.sheet.categoryNew') },
  ]

  const save = () => {
    const body: Parameters<typeof patchChannels>[0] = { channelIds: [c.channelId] }
    const n = name.trim()
    if (n !== (c.name === c.providerName ? '' : c.name)) body.name = n
    const cat = category === NEW ? newCategory.trim() : category === PLAYLIST ? '' : category
    if (category === NEW && !cat) return
    if (cat !== (c.category === c.group ? '' : c.category)) body.category = cat
    if (number !== (c.numberManual ? c.number : 0)) body.number = number
    if (shown !== c.shown) body.shown = shown
    if (Object.keys(body).length === 1) return onSaved()
    setBusy(true)
    setError('')
    setNumberError('')
    patchChannels(body).then(
      () => onSaved(t('iptv.sheet.saved', { name: n || c.providerName })),
      (e: unknown) => {
        setBusy(false)
        if (e instanceof ApiError && e.status === 409) setNumberError(t('iptv.sheet.numberTaken', { number }))
        else setError(errorText(e, t('common.genericError')))
      },
    )
  }

  const variantWord = (mode: 'own' | 'with' | 'auto', withMember?: number) => {
    setBusy(true)
    setError('')
    setVariant(sourceId, c.memberId, mode, withMember).then(
      () => onSaved(t('iptv.sheet.variantSaved', { name: c.providerName })),
      (e: unknown) => {
        setBusy(false)
        setError(errorText(e, t('common.genericError')))
      },
    )
  }

  if (binding) return <EPGBindSheet c={c} onBack={() => setBinding(false)} onBound={(m) => onSaved(m)} />
  if (merging) return <VariantMergeSheet c={c} all={all} onBack={() => setMerging(false)} onChoose={(m) => variantWord('with', m.memberId)} />

  const v = c.variant

  return (
    <Sheet
      title={c.name}
      message={c.name !== c.providerName ? t('iptv.sheet.inPlaylist', { name: c.providerName }) : undefined}
      role="dialog"
      size="wide"
      busy={busy}
      onCancel={onClose}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onClose },
        { label: t('iptv.editor.save'), kind: 'primary', disabled: category === NEW && !newCategory.trim(), run: save },
      ]}
    >
      <div className={styles.form}>
        <TextField label={t('iptv.sheet.name')} value={name} placeholder={c.providerName} hint={t('iptv.sheet.nameHint')} onChange={(e) => setName(e.target.value)} />
        <GroupedSection>
          <ListRow label={t('iptv.table.shown')}>
            <Toggle label={t('iptv.table.show', { name: c.name })} checked={shown} onChange={setShown} />
          </ListRow>
          <ListRow label={t('iptv.table.category')} stack>
            <Select label={t('iptv.table.category')} placement="bottom-end" value={category} options={options} onChange={setCategory} />
          </ListRow>
          {category === NEW && (
            <ListRow label={t('iptv.sheet.categoryName')} wide>
              <TextField label={t('iptv.sheet.categoryName')} hideLabel size="small" value={newCategory} onChange={(e) => setNewCategory(e.target.value)} autoFocus />
            </ListRow>
          )}
          <ListRow label={t('iptv.sheet.number')} hint={numberError ? <span className={styles.error}>{numberError}</span> : number ? t('iptv.sheet.numberManual') : t('iptv.sheet.numberAuto', { number: c.number })}>
            <NumberField label={t('iptv.sheet.number')} value={number} min={0} max={9999} chars={3} onChange={setNumber} />
          </ListRow>
          <ListRow
            label={t('iptv.table.epg')}
            hint={c.epg.how ? `${c.epg.xmlId} · ${epgLabel(t, c)}` : t('iptv.how.none')}
          >
            <Button variant="secondary" size="small" onClick={() => setBinding(true)}>
              {c.epg.how ? t('iptv.sheet.epgChange') : t('iptv.sheet.epgBind')}
            </Button>
          </ListRow>
          <ChannelLogoRow channelId={c.channelId} url={logo} onChanged={setLogo} pending={pending} onPending={setPending} />
        </GroupedSection>
        {variantsOn && c.present && (
          <GroupedSection
            title={t('iptv.sheet.variants')}
            footer={v?.manual === 'own' ? t('iptv.sheet.variantHow.own') : v?.how ? t(`iptv.sheet.variantHow.${v.how}`) : t('iptv.sheet.variantsNone')}
          >
            {v?.members.map((m, i) => (
              <ListRow
                key={m.memberId}
                label={
                  <span className={styles.nameLine}>
                    <span>{m.name}</span>
                    {i === 0 && <Badge tone="accent">{t('iptv.sheet.variantPrimary')}</Badge>}
                    {m.memberId === c.memberId && <Badge>{t('iptv.sheet.variantThis')}</Badge>}
                  </span>
                }
              />
            ))}
            {(v?.members.length ?? 0) > 0 && (
              <ListRow label={t('iptv.sheet.variantSplit')} hint={t('iptv.sheet.variantSplitHint')}>
                <Button variant="secondary" size="small" onClick={() => variantWord('own')}>
                  {t('iptv.sheet.variantSplitAction')}
                </Button>
              </ListRow>
            )}
            <ListLinkRow label={t('iptv.sheet.variantMerge')} onOpen={() => setMerging(true)} />
            {v?.manual && (
              <ListRow label={t('iptv.sheet.variantAuto')} hint={t('iptv.sheet.variantAutoHint')}>
                <Button variant="secondary" size="small" onClick={() => variantWord('auto')}>
                  {t('iptv.sheet.variantAutoAction')}
                </Button>
              </ListRow>
            )}
          </GroupedSection>
        )}
        {c.present && (
          <GroupedSection>
            <ListRow label={t('iptv.sheet.remove')} hint={t('iptv.sheet.removeHint')}>
              <Button variant="destructive" size="small" onClick={() => onRemove(c)}>
                {t('iptv.sheet.removeAction')}
              </Button>
            </ListRow>
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

/** «Объединить с…»: a search over the playlist's other channels; the chosen one's channel gets this entry as a variant. */
function VariantMergeSheet({ c, all, onBack, onChoose }: { c: SourceChannel; all: SourceChannel[]; onBack: () => void; onChoose: (m: SourceChannel) => void }) {
  const t = useT()
  const [q, setQ] = useState('')
  const needle = q.trim().toLowerCase()
  const peers = new Set(c.variant?.members.map((m) => m.memberId) ?? [])
  const hits = needle
    ? all.filter((x) => x.present && x.memberId !== c.memberId && !peers.has(x.memberId) && (x.providerName + '\n' + x.name).toLowerCase().includes(needle)).slice(0, 30)
    : []
  return (
    <Sheet
      title={t('iptv.sheet.variantTitle', { name: c.providerName })}
      message={t('iptv.sheet.variantMergeHint')}
      role="dialog"
      size="wide"
      onCancel={onBack}
      actions={[{ label: t('iptv.sheet.back'), kind: 'cancel', run: onBack }]}
    >
      <div className={styles.form}>
        <TextField type="search" label={t('iptv.sheet.variantSearch')} hideLabel placeholder={t('iptv.sheet.variantSearch')} value={q} onChange={(e) => setQ(e.target.value)} onClear={() => setQ('')} autoFocus />
        {needle && hits.length === 0 && <p className={styles.found}>{t('iptv.table.empty')}</p>}
        {hits.length > 0 && (
          <GroupedSection>
            {hits.map((h) => (
              <ListRow key={h.memberId} label={h.providerName} hint={[h.name !== h.providerName ? h.name : '', h.group].filter(Boolean).join(' · ')}>
                <IconButton size="sm" icon="plus" label={t('iptv.sheet.variantChoose', { name: h.providerName })} onClick={() => onChoose(h)} />
              </ListRow>
            ))}
          </GroupedSection>
        )}
      </div>
    </Sheet>
  )
}

/** The guide's search: bind the channel to one of the guides' channels by hand, or give it back to the automatic match. */
function EPGBindSheet({ c, onBack, onBound }: { c: SourceChannel; onBack: () => void; onBound: (message: string) => void }) {
  const t = useT()
  const [q, setQ] = useState(c.providerName)
  const [hits, setHits] = useState<EPGHit[] | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    const needle = q.trim()
    if (!needle) {
      setHits(null)
      return
    }
    const ac = new AbortController()
    const tm = window.setTimeout(() => {
      searchEPG(needle, ac.signal).then(setHits, () => undefined)
    }, 250)
    return () => {
      window.clearTimeout(tm)
      ac.abort()
    }
  }, [q])
  const bind = (h: EPGHit) =>
    bindEPG(c.channelId, h.epgUrlId, h.xmlId).then(
      () => onBound(t('iptv.sheet.epgBound', { name: c.name, guide: h.names[0] ?? h.xmlId })),
      (e: unknown) => setError(errorText(e, t('common.genericError'))),
    )
  const unbind = () =>
    unbindEPG(c.channelId).then(
      () => onBound(t('iptv.sheet.epgUnbound', { name: c.name })),
      (e: unknown) => setError(errorText(e, t('common.genericError'))),
    )
  return (
    <Sheet
      title={t('iptv.sheet.epgTitle', { name: c.name })}
      message={c.epg.how ? t('iptv.sheet.epgNow', { guide: c.epg.xmlId, how: epgLabel(t, c) }) : t('iptv.how.none')}
      role="dialog"
      size="wide"
      onCancel={onBack}
      actions={[{ label: t('iptv.sheet.back'), kind: 'cancel', run: onBack }]}
    >
      <div className={styles.form}>
        <TextField type="search" label={t('iptv.sheet.epgSearch')} hideLabel placeholder={t('iptv.sheet.epgSearch')} value={q} onChange={(e) => setQ(e.target.value)} onClear={() => setQ('')} autoFocus />
        {c.epg.manual && (
          <Button variant="link" onClick={() => void unbind()}>
            {t('iptv.sheet.epgUnbind')}
          </Button>
        )}
        {hits && hits.length === 0 && <p className={styles.found}>{t('iptv.table.empty')}</p>}
        {hits && hits.length > 0 && (
          <GroupedSection>
            {hits.map((h) => {
              const current = h.xmlId === c.epg.xmlId && h.epgUrlId === c.epg.epgUrlId
              return (
                <ListRow
                  key={`${h.epgUrlId}:${h.xmlId}`}
                  label={
                    <span className={styles.nameLine}>
                      <span>{h.names[0] ?? h.xmlId}</span>
                      {current && <Badge tone="accent">{t('iptv.sheet.epgCurrent')}</Badge>}
                    </span>
                  }
                  hint={[...h.names.slice(1, 3), h.xmlId].join(' · ')}
                >
                  {current ? (
                    <Icon name="check" size={18} className={styles.ok} />
                  ) : (
                    <IconButton size="sm" icon="plus" label={t('iptv.sheet.epgChoose', { name: h.names[0] ?? h.xmlId })} onClick={() => void bind(h)} />
                  )}
                </ListRow>
              )
            })}
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
