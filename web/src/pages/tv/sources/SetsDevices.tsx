// «Наборы» and «Устройства» of «Внешние источники». A set is which sources a
// device sees (Antresol's own channels are in every one); «Все мои каналы»
// (id 0) is the default and cannot change. Devices are the profiles' app
// sign-ins (iPhone, Apple TV, Android TV…) and their IPTV links; each gets
// a set from the pop-up at the row's end. «Новая ссылка для TiviMate»
// makes a link for a profile with a set and shows its two addresses.
import { useState } from 'react'
import { ApiError, errorText } from '../../../api/client'
import { assignDevice, createLink, deleteLink, deleteSet, type ChannelSet, type Device, type Source } from '../../../api/iptv'
import type { User } from '../../../api/types'
import { Icon, type IconName } from '../../../components/Icon'
import { useT } from '../../../i18n'
import { ago } from '../../../state/format'
import { useIsCompact } from '../../../state/useMediaQuery'
import { Badge } from '../../../ui/Badge'
import { Button } from '../../../ui/Button'
import { IconButton } from '../../../ui/IconButton'
import { GroupedSection, ListRow } from '../../../ui/List'
import { Menu, MenuItem, MenuSeparator } from '../../../ui/Menu'
import { Select } from '../../../ui/Select'
import { Sheet } from '../../../ui/Sheet'
import { TextField } from '../../../ui/TextField'
import { SetSheet } from './SetSheet'
import styles from './Sources.module.css'

const GLYPH: Record<Device['client'], IconName> = { tvos: 'tv', androidtv: 'tv', macos: 'tv', ios: 'play', ipados: 'play', android: 'play', iptv: 'broadcast', other: 'play' }

const copy = (text: string) => navigator.clipboard?.writeText(text).catch(() => undefined)

export function SetsPanel({ sets, sources, devices, onSets, onToast }: { sets: ChannelSet[]; sources: Source[]; devices: Device[]; onSets: (s: ChannelSet[]) => void; onToast: (s: string) => void }) {
  const t = useT()
  const [editing, setEditing] = useState<ChannelSet | 'new' | null>(null)
  const [removing, setRemoving] = useState<ChannelSet | null>(null)
  const used = (id: number) => devices.filter((d) => d.setId === id).length
  const usedLine = (id: number) => (used(id) ? t('iptv.setDevices', { count: used(id) }) : t('iptv.setNoDevices'))
  const names = (ids: number[]) => [t('iptv.ownChannels'), ...ids.map((id) => sources.find((x) => x.id === id)?.name ?? '')].filter(Boolean).join(' · ')
  return (
    <>
      <GroupedSection title={t('iptv.sets')} footer={t('iptv.setsFoot')}>
        <ListRow
          label={
            <span className={styles.nameLine}>
              <span>{t('iptv.setDefault')}</span>
              <Badge>{t('iptv.setDefaultBadge')}</Badge>
            </span>
          }
          hint={
            <>
              <span className={styles.line}>{t('iptv.setDefaultHint')}</span>
              <span className={styles.line}>{usedLine(0)}</span>
            </>
          }
        />
        {sets.map((s) => (
          <ListRow
            key={s.id}
            label={s.name}
            hint={
              <>
                <span className={styles.line}>{names(s.sources)}</span>
                <span className={styles.line}>{usedLine(s.id)}</span>
              </>
            }
          >
            <Menu label={t('iptv.setMore', { name: s.name })} placement="bottom-end" trigger={(p) => <IconButton {...p} size="sm" icon="ellipsis" label={t('iptv.setMore', { name: s.name })} />}>
              {(close) => (
                <>
                  <MenuItem icon={<Icon name="pencil" size={16} />} label={t('iptv.setEdit')} onSelect={() => (close(false), setEditing(s))} />
                  <MenuSeparator />
                  <MenuItem destructive icon={<Icon name="trash" size={16} />} label={t('iptv.setDelete')} onSelect={() => (close(false), setRemoving(s))} />
                </>
              )}
            </Menu>
          </ListRow>
        ))}
        <li className={styles.addRow}>
          <Button variant="plain" icon="plus" onClick={() => setEditing('new')}>
            {t('iptv.setNew')}
          </Button>
        </li>
      </GroupedSection>
      {editing && (
        <SetSheet
          set={editing === 'new' ? null : editing}
          sources={sources}
          onClose={() => setEditing(null)}
          onSaved={(s) => {
            setEditing(null)
            onSets(editing === 'new' ? [...sets, s] : sets.map((x) => (x.id === s.id ? s : x)))
          }}
        />
      )}
      {removing && (
        <Sheet
          title={t('iptv.setDeleteTitle', { name: removing.name })}
          message={t('iptv.setDeleteMessage')}
          icon={<Icon name="trash" size={34} strokeWidth={1.6} />}
          onCancel={() => setRemoving(null)}
          actions={[
            { label: t('common.cancel'), kind: 'cancel', run: () => setRemoving(null) },
            {
              label: t('iptv.setDelete'),
              kind: 'destructive',
              run: () => {
                const s = removing
                setRemoving(null)
                deleteSet(s.id).then(
                  () => onSets(sets.filter((x) => x.id !== s.id)),
                  (e: unknown) => onToast(errorText(e, t('common.genericError'))),
                )
              },
            },
          ]}
        />
      )}
    </>
  )
}

export function DevicesPanel({
  sets,
  devices,
  users,
  onDevices,
  onReload,
  onToast,
}: {
  sets: ChannelSet[]
  devices: Device[]
  users: User[]
  onDevices: (d: Device[]) => void
  onReload: () => void
  onToast: (s: string) => void
}) {
  const t = useT()
  const compact = useIsCompact()
  const [link, setLink] = useState(false)
  const [revoking, setRevoking] = useState<Device | null>(null)
  const options = [{ value: '0', label: t('iptv.setDefault') }, ...sets.map((s) => ({ value: String(s.id), label: s.name }))]
  const assign = (d: Device, setId: number) => {
    onDevices(devices.map((x) => (x === d ? { ...x, setId } : x)))
    assignDevice(d.kind === 'iptv' ? { linkId: d.linkId, setId } : { userId: d.userId, deviceId: d.deviceId, setId }).then(
      () => onToast(t('iptv.assigned', { device: d.name, set: options.find((o) => o.value === String(setId))?.label ?? '' })),
      (e: unknown) => {
        onToast(errorText(e, t('common.genericError')))
        onReload()
      },
    )
  }
  const clientName = (d: Device) => (d.client === 'other' ? d.clientName : t(`iptv.client.${d.client}`))
  return (
    <>
      <GroupedSection title={t('iptv.devices')} footer={t('iptv.devicesFoot')}>
        {devices.length === 0 && <ListRow label={t('iptv.devicesEmpty')} hint={t('iptv.devicesEmptyHint')} />}
        {devices.map((d) => (
          <ListRow
            key={d.kind === 'iptv' ? `l${d.linkId}` : `${d.userId}:${d.deviceId}`}
            stack
            leading={
              <span className={styles.glyph}>
                <Icon name={GLYPH[d.client] ?? 'play'} size={18} />
              </span>
            }
            label={
              <span className={styles.nameLine}>
                <span>{d.name || clientName(d)}</span>
                <Badge>{d.kind === 'iptv' ? t('iptv.kindIptv') : t('iptv.kindApp')}</Badge>
              </span>
            }
            hint={
              <>
                {`${d.profile} · ${clientName(d)} · `}
                <span className={styles.nowrap}>{d.lastSeen ? t('iptv.deviceSeen', { ago: ago(d.lastSeen) }) : t('iptv.deviceNeverSeen')}</span>
              </>
            }
          >
            <Select label={t('iptv.deviceSet')} showLabel={compact} placement="bottom-end" value={String(d.setId)} options={options} onChange={(v) => assign(d, Number(v))} />
            {d.kind === 'iptv' && (
              <Menu label={t('iptv.linkMore', { name: d.name })} placement="bottom-end" trigger={(p) => <IconButton {...p} size="sm" icon="ellipsis" label={t('iptv.linkMore', { name: d.name })} />}>
                {(close) => (
                  <>
                    {d.playlist && <MenuItem label={t('iptv.copyPlaylist')} onSelect={() => (close(), void copy(d.playlist!), onToast(t('iptv.copied')))} />}
                    {d.guide && <MenuItem label={t('iptv.copyGuide')} onSelect={() => (close(), void copy(d.guide!), onToast(t('iptv.copied')))} />}
                    {(d.playlist || d.guide) && <MenuSeparator />}
                    <MenuItem destructive icon={<Icon name="trash" size={16} />} label={t('iptv.linkDelete')} onSelect={() => (close(false), setRevoking(d))} />
                  </>
                )}
              </Menu>
            )}
          </ListRow>
        ))}
        <li className={styles.addRow}>
          <Button variant="plain" icon="plus" onClick={() => setLink(true)}>
            {t('iptv.newLink')}
          </Button>
        </li>
      </GroupedSection>
      {revoking && (
        <Sheet
          title={t('iptv.linkDeleteTitle', { name: revoking.name })}
          message={t('iptv.linkDeleteMessage')}
          icon={<Icon name="trash" size={34} strokeWidth={1.6} />}
          onCancel={() => setRevoking(null)}
          actions={[
            { label: t('common.cancel'), kind: 'cancel', run: () => setRevoking(null) },
            {
              label: t('iptv.linkDelete'),
              kind: 'destructive',
              run: () => {
                const d = revoking
                setRevoking(null)
                deleteLink(d.linkId).then(
                  () => {
                    onDevices(devices.filter((x) => !(x.kind === 'iptv' && x.linkId === d.linkId)))
                    onToast(t('iptv.linkDeleted', { name: d.name }))
                  },
                  (e: unknown) => onToast(errorText(e, t('common.genericError'))),
                )
              },
            },
          ]}
        />
      )}
      {link && (
        <LinkSheet
          sets={options}
          users={users}
          onClose={() => {
            setLink(false)
            onReload()
          }}
        />
      )}
    </>
  )
}

function LinkSheet({ sets, users, onClose }: { sets: { value: string; label: string }[]; users: User[]; onClose: () => void }) {
  const t = useT()
  const profiles = users.map((u) => ({ value: String(u.id), label: u.name }))
  const [user, setUser] = useState(profiles[0]?.value ?? '')
  const [set, setSet] = useState('0')
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [made, setMade] = useState<{ playlist: string; guide: string } | null>(null)
  const create = () => {
    const n = name.trim()
    if (!n || !user) return
    setBusy(true)
    setError('')
    createLink({ userId: Number(user), setId: Number(set), name: n }).then(
      (r) => {
        setBusy(false)
        setMade(r)
      },
      (e: unknown) => {
        setBusy(false)
        setError(e instanceof ApiError && e.status === 409 ? t('iptv.linkOff') : errorText(e, t('common.genericError')))
      },
    )
  }
  if (made)
    return (
      <Sheet title={t('iptv.linkReady', { name: name.trim() })} message={t('iptv.linkReadyMessage')} role="dialog" wide onCancel={onClose} actions={[{ label: t('iptv.done'), kind: 'primary', run: onClose }]}>
        <div className={styles.form}>
          <GroupedSection>
            <LinkField label={t('iptv.linkPlaylist')} value={made.playlist} />
            <LinkField label={t('iptv.linkGuide')} value={made.guide} />
          </GroupedSection>
        </div>
      </Sheet>
    )
  return (
    <Sheet
      title={t('iptv.linkTitle')}
      message={t('iptv.linkMessage')}
      role="dialog"
      wide
      busy={busy}
      onCancel={onClose}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onClose },
        { label: t('iptv.linkCreate'), kind: 'primary', disabled: !name.trim() || !user, run: create },
      ]}
    >
      <div className={styles.form}>
        <TextField label={t('iptv.linkName')} placeholder={t('iptv.linkNamePlaceholder')} value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        <GroupedSection>
          <ListRow label={t('iptv.linkProfile')}>
            <Select label={t('iptv.linkProfile')} placement="bottom-end" value={user} options={profiles} onChange={setUser} />
          </ListRow>
          <ListRow label={t('iptv.linkSet')}>
            <Select label={t('iptv.linkSet')} placement="bottom-end" value={set} options={sets} onChange={setSet} />
          </ListRow>
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

function LinkField({ label, value }: { label: string; value: string }) {
  const t = useT()
  const [copied, setCopied] = useState(false)
  return (
    <ListRow label={label} hint={<span className={styles.url}>{value}</span>}>
      <Button
        variant="secondary"
        size="small"
        icon={copied ? 'check' : undefined}
        onClick={() => {
          void copy(value)
          setCopied(true)
        }}
      >
        {copied ? t('iptv.copied') : t('iptv.copy')}
      </Button>
    </ListRow>
  )
}
