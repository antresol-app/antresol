// One external channel (a tap on a phone's row, «Изменить…» in a row's «⋯»):
// its name (empty: the playlist's), category (a picker over the categories,
// «Из плейлиста» or «Новая категория…»), number (0: automatic), the switch,
// its logo, and its guide — what it is bound to now, a search over the
// guides' channels to bind it by hand, «Отвязать» to give it back to the
// automatic match. Saving sends only what changed.
import { useEffect, useState } from 'react'
import { ApiError, errorText } from '../../../api/client'
import { bindEPG, getCategories, patchChannels, searchEPG, unbindEPG, type EPGHit, type SourceChannel } from '../../../api/iptv'
import { Icon } from '../../../components/Icon'
import { useT } from '../../../i18n'
import { Badge } from '../../../ui/Badge'
import { Button } from '../../../ui/Button'
import { IconButton } from '../../../ui/IconButton'
import { GroupedSection, ListRow } from '../../../ui/List'
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

export function ChannelSheet({ c, onClose, onSaved }: { c: SourceChannel; onClose: () => void; onSaved: (message?: string) => void }) {
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

  if (binding) return <EPGBindSheet c={c} onBack={() => setBinding(false)} onBound={(m) => onSaved(m)} />

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
        {error && (
          <p className={styles.formError} role="alert">
            {error}
          </p>
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
