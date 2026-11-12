// A source's settings (a large sheet): name, kind (Xtream Codes only when
// the server allows it), the playlist's address or the Xtream server, user
// and password — a stored secret is never shown: its field is empty with
// the mask as the placeholder and is sent only when typed — the channels'
// defaults (visibility by size, the presets, hiding the provider's
// addresses), the profiles that see it, the refresh interval and «при
// открытии», and the guides in priority order (add, remove, move). A
// refresh waiting for confirmation is answered here too.
import { useState, type ReactNode } from 'react'
import { ApiError, errorText } from '../../../api/client'
import { REFRESH_HOURS, type RefreshHours, type Source, type SourceInput } from '../../../api/iptv'
import type { User } from '../../../api/types'
import { Icon } from '../../../components/Icon'
import { useT, type TFunction } from '../../../i18n'
import { Button } from '../../../ui/Button'
import { IconButton } from '../../../ui/IconButton'
import { GroupedSection, ListRow } from '../../../ui/List'
import { Menu, MenuItem, MenuSeparator } from '../../../ui/Menu'
import { SegmentedControl } from '../../../ui/SegmentedControl'
import { MultiSelect, Select } from '../../../ui/Select'
import { Sheet } from '../../../ui/Sheet'
import { TextField } from '../../../ui/TextField'
import { Toggle } from '../../../ui/Toggle'
import styles from './Sources.module.css'

export const intervalOptions = (t: TFunction) => REFRESH_HOURS.map((h) => ({ value: String(h) as `${RefreshHours}`, label: t(`iptv.interval.${h}`) }))

type Shown = 'auto' | 'shown' | 'hidden'
interface Guide {
  key: string
  id?: number
  /** Masked (a saved one) or typed (a new one). */
  url: string
  auto: boolean
  refreshHours: RefreshHours
  refreshOnOpen: boolean
}

export function SourceEditor({
  source: s,
  xtreamEnabled,
  users,
  onClose,
  onSave,
  drop,
}: {
  source: Source | null
  xtreamEnabled: boolean
  users: User[]
  onClose: () => void
  onSave: (body: SourceInput) => Promise<void>
  /** The pending-drop notice, when one waits. */
  drop?: ReactNode
}) {
  const t = useT()
  const [name, setName] = useState(s?.name ?? '')
  const [kind, setKind] = useState<'m3u' | 'xtream'>(s?.kind ?? 'm3u')
  const [url, setUrl] = useState('')
  const [server, setServer] = useState(s?.xtream?.server ?? '')
  const [user, setUser] = useState(s?.xtream?.user ?? '')
  const [password, setPassword] = useState('')
  const [shown, setShown] = useState<Shown>(s?.defaultShown == null ? 'auto' : s.defaultShown ? 'shown' : 'hidden')
  const [timeshift, setTimeshift] = useState(s?.presets.timeshift ?? true)
  const [adult, setAdult] = useState(s?.presets.adult ?? true)
  const [hideUrls, setHideUrls] = useState(s?.hideUrls ?? false)
  const [hours, setHours] = useState<RefreshHours>(s?.refreshHours ?? 24)
  const [onOpen, setOnOpen] = useState(s?.refreshOnOpen ?? true)
  const [profiles, setProfiles] = useState<string[]>((s?.profiles ?? []).map(String))
  const [guides, setGuides] = useState<Guide[]>(
    [...(s?.epg ?? [])].sort((a, b) => a.position - b.position).map((g) => ({ key: `g${g.id}`, id: g.id, url: g.url, auto: g.auto, refreshHours: g.refreshHours, refreshOnOpen: g.refreshOnOpen })),
  )
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const isNew = !s
  const missing = !name.trim() || (isNew && (kind === 'm3u' ? !url.trim() : !server.trim() || !user.trim() || !password))
  const move = (i: number, step: number) =>
    setGuides((g) => {
      const n = [...g]
      const [x] = n.splice(i, 1)
      n.splice(i + step, 0, x!)
      return n
    })
  const patchGuide = (i: number, p: Partial<Guide>) => setGuides((g) => g.map((x, k) => (k === i ? { ...x, ...p } : x)))

  const save = () => {
    if (missing) return
    const body: SourceInput = {
      name: name.trim(),
      kind,
      defaultShown: shown === 'auto' ? null : shown === 'shown',
      hideUrls,
      presets: { timeshift, adult },
      refreshHours: hours,
      refreshOnOpen: onOpen,
      profiles: profiles.map(Number),
      epg: guides.filter((g) => g.id || g.url.trim()).map((g) => (g.id ? { id: g.id, refreshHours: g.refreshHours, refreshOnOpen: g.refreshOnOpen } : { url: g.url.trim(), refreshHours: g.refreshHours, refreshOnOpen: g.refreshOnOpen })),
    }
    if (kind === 'm3u' && url.trim()) body.url = url.trim()
    if (kind === 'xtream') body.xtream = { server: server.trim(), user: user.trim(), password }
    setBusy(true)
    setError('')
    onSave(body).catch((e: unknown) => {
      setBusy(false)
      setError(e instanceof ApiError && e.status === 400 ? errorText(e, t('iptv.editor.invalid')) : errorText(e, t('common.genericError')))
    })
  }

  const profileOptions = users.filter((u) => !u.isKid).map((u) => ({ value: String(u.id), label: u.name }))

  return (
    <Sheet
      title={isNew ? t('iptv.editor.newTitle') : t('iptv.editor.title', { name: s.name })}
      role="dialog"
      size="large"
      busy={busy}
      onCancel={onClose}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onClose },
        { label: isNew ? t('iptv.editor.create') : t('iptv.editor.save'), kind: 'primary', disabled: missing, run: save },
      ]}
    >
      <div className={styles.form}>
        {drop}
        <TextField label={t('iptv.editor.name')} value={name} placeholder={t('iptv.editor.namePlaceholder')} onChange={(e) => setName(e.target.value)} autoFocus={isNew} />
        {(xtreamEnabled || kind === 'xtream') && (
          <SegmentedControl
            label={t('iptv.editor.kind')}
            value={kind}
            onChange={setKind}
            segments={[
              { value: 'm3u', label: t('iptv.kind.m3u') },
              { value: 'xtream', label: t('iptv.kind.xtream') },
            ]}
          />
        )}
        {kind === 'm3u' ? (
          <TextField
            label={t('iptv.editor.url')}
            type="url"
            inputMode="url"
            autoComplete="off"
            spellCheck={false}
            value={url}
            placeholder={s?.url || 'http://…/playlist.m3u'}
            hint={isNew ? t('iptv.editor.urlHint') : t('iptv.editor.secretKeep')}
            onChange={(e) => setUrl(e.target.value)}
          />
        ) : (
          <>
            <TextField label={t('iptv.editor.server')} type="url" autoComplete="off" spellCheck={false} value={server} placeholder="http://…:8080" onChange={(e) => setServer(e.target.value)} />
            <TextField label={t('iptv.editor.user')} autoComplete="off" spellCheck={false} value={user} onChange={(e) => setUser(e.target.value)} />
            <TextField
              label={t('iptv.editor.password')}
              type="password"
              revealable
              autoComplete="new-password"
              value={password}
              placeholder={isNew ? '' : '••••••••'}
              hint={isNew ? undefined : t('iptv.editor.secretKeep')}
              onChange={(e) => setPassword(e.target.value)}
            />
          </>
        )}

        <GroupedSection title={t('iptv.editor.channels')}>
          <ListRow label={t('iptv.editor.defaultShown')} hint={t('iptv.editor.defaultShownHint')} stack>
            <Select
              label={t('iptv.editor.defaultShown')}
              placement="bottom-end"
              value={shown}
              onChange={setShown}
              options={[
                { value: 'auto', label: t('iptv.editor.shownAuto') },
                { value: 'shown', label: t('iptv.editor.shownYes') },
                { value: 'hidden', label: t('iptv.editor.shownNo') },
              ]}
            />
          </ListRow>
          <ListRow label={t('iptv.editor.presetTimeshift')} hint={t('iptv.editor.presetTimeshiftHint')}>
            <Toggle label={t('iptv.editor.presetTimeshift')} checked={timeshift} onChange={setTimeshift} />
          </ListRow>
          <ListRow label={t('iptv.editor.presetAdult')} hint={t('iptv.editor.presetAdultHint')}>
            <Toggle label={t('iptv.editor.presetAdult')} checked={adult} onChange={setAdult} />
          </ListRow>
          <ListRow label={t('iptv.editor.hideUrls')} hint={t('iptv.editor.hideUrlsHint')}>
            <Toggle label={t('iptv.editor.hideUrls')} checked={hideUrls} onChange={setHideUrls} />
          </ListRow>
          <ListRow label={t('iptv.editor.profiles')} hint={t('iptv.editor.profilesHint')} stack>
            <MultiSelect label={t('iptv.editor.profiles')} placement="bottom-end" value={profiles} onChange={setProfiles} options={profileOptions} none={t('iptv.editor.profilesAll')} />
          </ListRow>
        </GroupedSection>

        <GroupedSection title={t('iptv.editor.refresh')}>
          <ListRow label={t('iptv.interval')}>
            <Select label={t('iptv.interval')} placement="bottom-end" value={String(hours) as `${RefreshHours}`} options={intervalOptions(t)} onChange={(v) => setHours(Number(v) as RefreshHours)} />
          </ListRow>
          <ListRow label={t('iptv.onOpen')} hint={t('iptv.onOpenHint')}>
            <Toggle label={t('iptv.onOpen')} checked={onOpen} onChange={setOnOpen} />
          </ListRow>
        </GroupedSection>

        <GroupedSection title={t('iptv.epgList')} footer={t('iptv.epgListFoot')}>
          {guides.map((g, i) => (
            <ListRow
              key={g.key}
              stack
              label={
                g.id ? (
                  <span className={styles.url}>{g.url}</span>
                ) : (
                  <TextField label={t('iptv.epgUrl')} hideLabel size="small" type="url" spellCheck={false} autoComplete="off" value={g.url} placeholder="http://…/epg.xml.gz" onChange={(e) => patchGuide(i, { url: e.target.value })} autoFocus />
                )
              }
              hint={g.auto ? t('iptv.epgAuto') : undefined}
            >
              <Select label={t('iptv.intervalEpg')} placement="bottom-end" value={String(g.refreshHours) as `${RefreshHours}`} options={intervalOptions(t)} onChange={(v) => patchGuide(i, { refreshHours: Number(v) as RefreshHours })} />
              <Menu label={t('iptv.epgMore')} placement="bottom-end" trigger={(p) => <IconButton {...p} size="sm" icon="ellipsis" label={t('iptv.epgMore')} />}>
                {(close) => (
                  <>
                    <MenuItem label={t('iptv.onOpen')} checked={g.refreshOnOpen} multi onSelect={() => (close(), patchGuide(i, { refreshOnOpen: !g.refreshOnOpen }))} />
                    <MenuSeparator />
                    <MenuItem icon={<Icon name="arrowUp" size={16} />} label={t('iptv.epgUp')} disabled={i === 0} onSelect={() => (close(), move(i, -1))} />
                    <MenuItem icon={<Icon name="arrowDown" size={16} />} label={t('iptv.epgDown')} disabled={i === guides.length - 1} onSelect={() => (close(), move(i, 1))} />
                    {!g.auto && (
                      <>
                        <MenuSeparator />
                        <MenuItem destructive icon={<Icon name="trash" size={16} />} label={t('iptv.epgRemove')} onSelect={() => (close(), setGuides((x) => x.filter((_, k) => k !== i)))} />
                      </>
                    )}
                  </>
                )}
              </Menu>
            </ListRow>
          ))}
          <li className={styles.addRow}>
            <Button variant="plain" icon="plus" onClick={() => setGuides((g) => [...g, { key: `n${Date.now()}`, url: '', auto: false, refreshHours: 24, refreshOnOpen: false }])}>
              {t('iptv.epgAdd')}
            </Button>
          </li>
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
