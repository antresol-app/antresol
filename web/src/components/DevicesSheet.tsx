// «Устройства»: the Vault app on iPhone, iPad and Mac and the browsers
// signed in to this profile (an admin: to every profile, by profile). Each one can be signed out from here (its
// session ends, with the media keys it asked for, e.g. for AirPlay; it is
// asked to sign in again). «Отдельная история на этом устройстве» switches
// a device to a history of its own (a managed account, listed below in
// «Отдельные истории»); turning it off asks whether to move that history
// into the profile or keep it apart. A kid profile sees the state only: its
// switch and accounts are an administrator's to change.
import { APP_NAME } from '../brand'
import { useCallback, useEffect, useState } from 'react'
import { getAllManagedAccounts, getDevices, getManagedAccounts, revokeDevice, setDeviceHistory, errorText } from '../api/client'
import type { Device, ManagedAccount } from '../api/types'
import { session } from '../state/session'
import { locale, t as tr, useT } from '../i18n'
import { Avatar } from './Avatar'
import { Badge } from '../ui/Badge'
import { Button } from '../ui/Button'
import { GroupedSection, ListRow } from '../ui/List'
import { Sheet } from '../ui/Sheet'
import { Toggle } from '../ui/Toggle'
import { ManagedAccountsSection } from './ManagedAccountsSection'
import styles from './DevicesSheet.module.css'

export interface DevicesSheetProps {
  onClose: () => void
}

type State = { status: 'loading' } | { status: 'error'; message: string } | { status: 'ready'; items: Device[]; managed: ManagedAccount[] }

const errText = (e: unknown) => (errorText(e, tr('comp.serverUnreachable')))

/** «Последний вход: 25 октября». */
const lastSeenLabel = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : tr('comp.devices.lastSeen', { date: d.toLocaleDateString(locale(), { day: 'numeric', month: 'long' }) })
}

const isWeb = (d: Device) => d.kind === 'web'
// a browser by its name and system («Safari · iPhone»); one signed in before
// the server kept them is just «Браузер»
const deviceName = (d: Device) => (isWeb(d) ? [d.name || tr('comp.devices.browser'), d.model].filter(Boolean).join(' · ') : d.name || APP_NAME)
const subtitle = (d: Device) =>
  isWeb(d) ? tr('comp.devices.webVersion', { app: APP_NAME }) : [d.version ? `${APP_NAME} ${d.version}` : APP_NAME, d.model].filter(Boolean).join(' · ')

interface Group {
  userId: number
  userName: string
  userAvatar: string
  userPhoto?: string
  items: Device[]
}

/** The devices by profile (an admin sees every profile's): this device's profile first, then in the order of their latest sign-in. */
function groups(items: Device[]): Group[] {
  const by = new Map<number, Group>()
  for (const d of items) {
    const id = d.userId ?? 0
    let g = by.get(id)
    if (!g) by.set(id, (g = { userId: id, userName: d.userName ?? '', userAvatar: d.userAvatar ?? '', userPhoto: d.userPhoto, items: [] }))
    g.items.push(d)
  }
  const mine = items.find((d) => d.current)?.userId ?? 0
  return [...by.values()].sort((a, b) => Number(b.userId === mine) - Number(a.userId === mine))
}

export function DevicesSheet({ onClose }: DevicesSheetProps) {
  const t = useT()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [confirm, setConfirm] = useState<Device | null>(null)
  const [turnOff, setTurnOff] = useState<Device | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const kid = Boolean(session.get().user?.isKid)

  // soft: keep the list on screen while it is read again (after a switch)
  const load = useCallback((soft = false) => {
    let live = true
    if (!soft) setState({ status: 'loading' })
    // an admin sees every profile's managed accounts; an older server has
    // none: the devices alone; a kid has no list of them
    const user = session.get().user
    const managed = user?.isKid ? Promise.resolve({ items: [] as ManagedAccount[] }) : user?.isAdmin ? getAllManagedAccounts() : getManagedAccounts()
    Promise.all([getDevices(), managed.catch(() => ({ items: [] as ManagedAccount[] }))])
      .then(([r, m]) => live && setState({ status: 'ready', items: r.items, managed: m.items }))
      .catch((e: unknown) => live && (soft ? setError(errText(e)) : setState({ status: 'error', message: errText(e) })))
    return () => {
      live = false
    }
  }, [])
  useEffect(() => load(), [load])
  const reload = useCallback(() => void load(true), [load])

  const signOut = (d: Device) => {
    setConfirm(null)
    setBusy(true)
    setError('')
    revokeDevice(d.id)
      .then(() => setState((s) => (s.status === 'ready' ? { ...s, items: s.items.filter((x) => x.id !== d.id) } : s)))
      .catch((e: unknown) => setError(errText(e)))
      .finally(() => setBusy(false))
  }

  // the switch: on at once, off after «Перенести в мой профиль» / «Оставить»
  const switchHistory = (d: Device, body: Parameters<typeof setDeviceHistory>[1]) => {
    setTurnOff(null)
    setBusy(true)
    setError('')
    setDeviceHistory(d.id, body)
      .then((r) => {
        // the profile menu shows this device's history
        if (d.current && d.userId) session.updateUser({ id: d.userId, deviceHistory: r.history })
        reload()
      })
      .catch((e: unknown) => setError(errorText(e, tr('comp.devices.separateFailed'))))
      .finally(() => setBusy(false))
  }

  return (
    <>
      <Sheet
        title={t('comp.devices.title')}
        role="dialog"
        size="wide"
        dismissOnOutside
        onCancel={onClose}
        closeButton={false}
        actions={[{ label: t('common.done'), kind: 'primary', run: onClose }]}
      >
        <div className={styles.body} tabIndex={-1} data-autofocus>
          {state.status === 'loading' && <p className={styles.note}>{t('common.loadingEllipsis')}</p>}
          {state.status === 'error' && (
            <div className={styles.retry} role="alert">
              <span className={styles.error}>{state.message}</span>
              <button type="button" className={styles.button} onClick={() => load()}>
                {t('common.retry')}
              </button>
            </div>
          )}
          {state.status === 'ready' && state.items.length === 0 && (
            <p className={styles.note}>{t('comp.devices.none')}</p>
          )}
          {state.status === 'ready' &&
            groups(state.items).map((g) => (
              <GroupedSection
                key={g.userId}
                id={`devices-${g.userId}`}
                title={
                  <span className={styles.profile}>
                    <Avatar name={g.userName} avatar={g.userAvatar} photo={g.userPhoto} size={22} />
                    {g.userName}
                  </span>
                }
              >
                {g.items.map((d) => (
                  <ListRow
                    key={d.id}
                    label={
                      <span className={styles.name}>
                        {deviceName(d)}
                        {d.current && <Badge>{t('comp.devices.current')}</Badge>}
                      </span>
                    }
                    hint={[subtitle(d), lastSeenLabel(d.lastSeen), d.history ? t('comp.devices.separateOn', { name: d.history.name }) : '']
                      .filter(Boolean)
                      .join(' · ')}
                  >
                    {!kid && (
                      <Toggle
                        label={`${t('comp.devices.separate')}: ${deviceName(d)}`}
                        checked={Boolean(d.history)}
                        disabled={busy}
                        size="small"
                        onChange={(on) => (on ? switchHistory(d, { separate: true }) : setTurnOff(d))}
                      />
                    )}
                    <Button
                      size="small"
                      variant="destructive"
                      disabled={busy}
                      aria-label={`${t('comp.devices.signOut')}: ${deviceName(d)}`}
                      onClick={() => setConfirm(d)}
                    >
                      {t('comp.devices.signOutAction')}
                    </Button>
                  </ListRow>
                ))}
              </GroupedSection>
            ))}
          {state.status === 'ready' && !kid && <ManagedAccountsSection items={state.managed} me={session.get().user?.id ?? 0} onChanged={reload} />}
          {error && (
            <p className={styles.error} role="alert">
              {error}
            </p>
          )}
        </div>
      </Sheet>

      {confirm && (
        <Sheet
          title={t('comp.devices.signOutTitle', { name: deviceName(confirm) })}
          onCancel={() => setConfirm(null)}
          actions={[
            { label: t('common.cancelAction'), kind: 'cancel', run: () => setConfirm(null) },
            { label: t('comp.devices.signOutAction'), kind: 'destructive', run: () => signOut(confirm) },
          ]}
        />
      )}

      {turnOff && (
        <Sheet
          title={t('comp.devices.offTitle', { name: deviceName(turnOff) })}
          message={t('comp.devices.offText')}
          onCancel={() => setTurnOff(null)}
          actions={[
            { label: t('common.cancelAction'), kind: 'cancel', run: () => setTurnOff(null) },
            { label: t('comp.devices.offKeep'), run: () => switchHistory(turnOff, { separate: false, merge: false }) },
            { label: t('comp.devices.offMerge'), kind: 'primary', run: () => switchHistory(turnOff, { separate: false, merge: true }) },
          ]}
        />
      )}
    </>
  )
}
