// «Профили»: who uses Vault. Create, rename, change the avatar, make a
// profile a kid's (with an age limit) or an administrator, set a password,
// set its «Звук и субтитры» and «Экранное время» (a kid's only here), delete
// (with a confirmation).
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useState } from 'react'
import { ApiError, createUser, deleteUser, getScreenLimited, getUsers, resetAppPassword, updateUser, type UserInput } from '../../api/client'
import type { ScreenLimited, User } from '../../api/types'
import { Avatar } from '../../components/Avatar'
import { Button } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { Icon } from '../../components/Icon'
import { PlaybackPrefsSheet } from '../../components/PlaybackPrefsSheet'
import { NotifySheet } from '../../components/NotifySheet'
import { ScreenTimeSheet } from '../../components/ScreenTimeSheet'
import { Sheet } from '../../ui/Sheet'
import { TextField } from '../../ui/TextField'
import { Menu, MenuItem, MenuSeparator } from '../../ui/Menu'
import { Select } from '../../ui/Select'
import { Toggle } from '../../ui/Toggle'
import { session, useSession } from '../../state/session'
import { useIsCompact } from '../../state/useMediaQuery'
import { t, useT } from '../../i18n'
import m from './Manage.module.css'
import styles from './Profiles.module.css'

const AVATARS = Array.from({ length: 8 }, (_, i) => `grad-${i + 1}`)
const RATINGS = ['0+', '6+', '12+', '16+', '18+']

function role(u: User) {
  if (u.isAdmin) return t('common.role.admin')
  if (u.isKid) return t('profiles.role.kid', { rating: u.maxRating || '12+' })
  return u.hasPassword ? t('profiles.role.password') : t('profiles.role.noPassword')
}

/** Profiles without a password sign in only from the home network. */
export const lanOnlyHint = () => t('profiles.lanOnlyHint')

type Edit = { user: User | null } | null

export function Profiles() {
  const t = useT()
  const { user: me } = useSession()
  const compact = useIsCompact()
  const [list, setList] = useState<User[] | null>(null)
  const [error, setError] = useState('')
  const [edit, setEdit] = useState<Edit>(null)
  const [removing, setRemoving] = useState<User | null>(null)
  const [playback, setPlayback] = useState<User | null>(null)
  // a kid's «Уведомления на почту» are the admin's (an adult sets its own)
  const [notify, setNotify] = useState<User | null>(null)
  // a kid's «Экранное время»: usage, limits, the day's bonus
  const [screen, setScreen] = useState<User | null>(null)

  // kids with screen-time rules and the profiles open without a password (a way around them)
  const [limited, setLimited] = useState<ScreenLimited | null>(null)
  const load = useCallback(() => {
    getUsers().then(setList, (e: unknown) => setError(e instanceof ApiError ? e.message : t('common.loadFailed')))
    getScreenLimited().then(setLimited, () => setLimited(null))
  }, [])
  const openTo = (kid: User) =>
    limited?.userIds.includes(kid.id) ? limited.passwordless.filter((p) => p.id !== kid.id).map((p) => p.name) : []
  useEffect(() => {
    document.title = `${t('manage.section.profiles')} — ${APP_NAME}`
    load()
  }, [load, t])

  return (
    <section aria-labelledby="profiles-title">
      <header className={m.header}>
        <div>
          <h2 id="profiles-title" className={m.title}>
            {t('manage.section.profiles')}
          </h2>
          <p className={m.subtitle}>{t('profiles.subtitle')}</p>
        </div>
        <div className={m.headerActions}>
          <Button icon="plus" onClick={() => setEdit({ user: null })}>
            {t('profiles.new')}
          </Button>
        </div>
      </header>
      {error && (
        <div className={m.problem} role="alert">
          {error}
        </div>
      )}
      {!list && !error && (
        <div className={m.spinnerRow} role="status">
          <span className={m.spinner} aria-hidden /> {t('downloads.loading')}
        </div>
      )}
      {list && (
        <ul className={m.group}>
          {list.map((u) => (
            <li key={u.id} className={m.row}>
              <Avatar name={u.name} avatar={u.avatar} size={40} />
              <div className={m.rowText}>
                <p className={m.rowLabel}>
                  {u.name}
                  {u.id === me?.id && <span className={styles.me}> · {t('profiles.you')}</span>}
                </p>
                <p className={m.rowHint}>{role(u)}</p>
                {u.isKid && openTo(u).length > 0 && (
                  <p className={styles.warning}>{t('comp.screen.passwordless', { names: openTo(u).join(', ') })}</p>
                )}
              </div>
              <div className={m.rowControl}>
                {compact ? (
                  // a phone: one «⋯» (five round buttons crushed the row's text)
                  <Menu
                    label={t('profiles.actions', { name: u.name })}
                    placement="bottom-end"
                    trigger={(p) => <IconButton {...p} size="sm" icon="ellipsis" label={t('profiles.actions', { name: u.name })} />}
                  >
                    {(close) => (
                      <>
                        <MenuItem icon={<Icon name="pencil" size={16} />} label={t('profiles.edit')} onSelect={() => (close(false), setEdit({ user: u }))} />
                        <MenuItem icon={<Icon name="captions" size={16} />} label={t('profiles.playback')} onSelect={() => (close(false), setPlayback(u))} />
                        {u.isKid && <MenuItem icon={<Icon name="send" size={16} />} label={t('profiles.notify')} onSelect={() => (close(false), setNotify(u))} />}
                        {u.isKid && <MenuItem icon={<Icon name="clock" size={16} />} label={t('comp.screen.title')} onSelect={() => (close(false), setScreen(u))} />}
                        <MenuSeparator />
                        <MenuItem
                          destructive
                          icon={<Icon name="trash" size={16} />}
                          label={t('comp.delete.menu')}
                          disabled={u.id === me?.id}
                          onSelect={() => (close(false), setRemoving(u))}
                        />
                      </>
                    )}
                  </Menu>
                ) : (
                  <>
                    <Button size="small" variant="secondary" icon="pencil" onClick={() => setEdit({ user: u })}>
                      {t('profiles.edit')}
                    </Button>
                    <IconButton size="sm" icon="captions" label={t('profiles.playbackAria', { name: u.name })} tooltip={t('profiles.playback')} onClick={() => setPlayback(u)} />
                    {u.isKid && (
                      <IconButton size="sm" icon="send" label={t('profiles.notifyAria', { name: u.name })} tooltip={t('profiles.notify')} onClick={() => setNotify(u)} />
                    )}
                    {u.isKid && (
                      <IconButton size="sm" icon="clock" label={t('comp.screen.aria', { name: u.name })} tooltip={t('comp.screen.title')} onClick={() => setScreen(u)} />
                    )}
                    <IconButton size="sm" icon="trash" label={t('profiles.deleteAria', { name: u.name })} disabled={u.id === me?.id} onClick={() => setRemoving(u)} />
                  </>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
      {edit && (
        <ProfileSheet
          user={edit.user}
          onClose={() => setEdit(null)}
          onSaved={(u) => {
            setEdit(null)
            load()
            if (u.id === me?.id) void session.bootstrap()
          }}
        />
      )}
      {playback && <PlaybackPrefsSheet userId={playback.id} name={playback.name} onClose={() => setPlayback(null)} />}
      {notify && <NotifySheet userId={notify.id} name={notify.name} onClose={() => setNotify(null)} />}
      {screen && <ScreenTimeSheet userId={screen.id} name={screen.name} onClose={() => setScreen(null)} />}
      {removing && (
        <Sheet
          title={t('profiles.deleteConfirm', { name: removing.name })}
          message={t('profiles.deleteText')}
          icon={<Icon name="trash" size={34} strokeWidth={1.6} />}
          onCancel={() => setRemoving(null)}
          actions={[
            { label: t('common.cancel'), kind: 'cancel', run: () => setRemoving(null) },
            {
              label: t('common.delete'),
              kind: 'destructive',
              run: () => {
                const u = removing
                setRemoving(null)
                deleteUser(u.id).then(load, (e: unknown) => setError(e instanceof ApiError ? e.message : t('mfiles.deleteFailed')))
              },
            },
          ]}
        />
      )}
    </section>
  )
}

function ProfileSheet({ user, onClose, onSaved }: { user: User | null; onClose: () => void; onSaved: (u: User) => void }) {
  const t = useT()
  const [name, setName] = useState(user?.name ?? '')
  const [avatar, setAvatar] = useState(user?.avatar ?? AVATARS[Math.floor(Math.random() * AVATARS.length)]!)
  const [kid, setKid] = useState(user?.isKid ?? false)
  const [admin, setAdmin] = useState(user?.isAdmin ?? false)
  const [rating, setRating] = useState(user?.maxRating ?? '')
  const [hidden, setHidden] = useState(user?.hideProgress ?? false)
  const [kindle, setKindle] = useState(user?.kindleEmail ?? '')
  const [password, setPassword] = useState('')
  const [dropPassword, setDropPassword] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [appReset, setAppReset] = useState('')
  const hasPassword = (user?.hasPassword ?? false) && !dropPassword
  const needsPassword = admin && !hasPassword && !password
  const tooShort = password !== '' && [...password].length < (admin ? 6 : 4)

  const save = async () => {
    setBusy(true)
    setError('')
    const body: Partial<UserInput> = { name: name.trim(), avatar, isKid: kid, isAdmin: admin, maxRating: kid ? rating : '' }
    if (user) {
      body.hideProgress = hidden
      body.kindleEmail = kindle.trim()
    }
    if (password) body.password = password
    else if (dropPassword) body.password = ''
    try {
      const u = user ? await updateUser(user.id, body) : await createUser(body as UserInput)
      onSaved(u)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : t('profiles.saveFailed'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Sheet
      title={user ? t('profiles.editTitle') : t('profiles.new')}
      role="dialog"
      size="wide"
      busy={busy}
      onCancel={onClose}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onClose },
        { label: user ? t('common.save') : t('common.create'), kind: 'primary', disabled: !name.trim() || needsPassword || tooShort, run: () => void save() },
      ]}
    >
      <div className={styles.form}>
        <div className={styles.preview}>
          <Avatar name={name || '?'} avatar={avatar} size={64} />
        </div>
        <div className={styles.avatars} role="radiogroup" aria-label={t('profiles.avatarColor')}>
          {AVATARS.map((a) => (
            <button
              key={a}
              type="button"
              role="radio"
              aria-checked={a === avatar}
              aria-label={t('profiles.color', { n: a.slice(5) })}
              className={styles.swatch}
              style={{ background: `var(--${a})` }}
              onClick={() => setAvatar(a)}
            />
          ))}
        </div>
        <TextField label={t('common.name')} value={name} onChange={(e) => setName(e.target.value)} autoComplete="off" />
        <div className={styles.switchRow}>
          <div>
            <p className={m.rowLabel}>{t('common.role.kid')}</p>
            <p className={m.rowHint}>{t(__LIBRARY_EDITION__ ? 'profiles.kidHintLibrary' : 'profiles.kidHint')}</p>
          </div>
          <Toggle label={t('common.role.kid')} checked={kid} onChange={(v) => (setKid(v), v && setAdmin(false))} />
        </div>
        {kid && (
          <div className={styles.switchRow}>
            <p className={m.rowLabel}>{t('profiles.maxAge')}</p>
            <Select
              label={t('profiles.ageLimit')}
              placement="bottom-end"
              value={rating}
              options={[{ value: '', label: t('profiles.ageDefault') }, ...RATINGS.map((r) => ({ value: r, label: r }))]}
              onChange={setRating}
            />
          </div>
        )}
        {!kid && (
          <div className={styles.switchRow}>
            <div>
              <p className={m.rowLabel}>{t('common.role.admin')}</p>
              <p className={m.rowHint}>{t('profiles.adminHint')}</p>
            </div>
            <Toggle label={t('common.role.admin')} checked={admin} onChange={setAdmin} />
          </div>
        )}
        {user && (
          <div className={styles.switchRow}>
            <div>
              <p className={m.rowLabel}>{t('profiles.hideProgress')}</p>
              <p className={m.rowHint}>{t('profiles.hideProgressHint')}</p>
            </div>
            <Toggle label={t('profiles.hideProgress')} checked={hidden} onChange={setHidden} />
          </div>
        )}
        {user && (
          <TextField
            label={t('profiles.kindleEmail')}
            hint={t('profiles.kindleEmailHint')}
            type="email"
            value={kindle}
            onChange={(e) => setKindle(e.target.value)}
            autoComplete="off"
            spellCheck={false}
          />
        )}
        <TextField
          label={hasPassword ? t('profiles.newPassword') : t('common.password')}
          type="password"
          revealable
          value={password}
          onChange={(e) => {
            setPassword(e.target.value)
            if (e.target.value) setDropPassword(false)
          }}
          autoComplete="new-password"
          error={tooShort ? t('profiles.tooShort', { n: admin ? 6 : 4 }) : undefined}
          hint={
            admin
              ? t('profiles.adminPassword')
              : dropPassword
                ? `${t('profiles.passwordDropped')} ${lanOnlyHint()}`
                : hasPassword
                  ? t('profiles.keepEmpty')
                  : `${t('profiles.optional')} ${lanOnlyHint()}`
          }
        />
        {user?.hasPassword && !admin && (
          <div className={styles.switchRow}>
            {dropPassword ? (
              <Button size="small" variant="secondary" onClick={() => setDropPassword(false)}>
                {t('profiles.keepPassword')}
              </Button>
            ) : (
              <Button
                size="small"
                variant="secondary"
                icon="lock"
                onClick={() => {
                  setDropPassword(true)
                  setPassword('')
                }}
              >
                {t('profiles.removePassword')}
              </Button>
            )}
          </div>
        )}
        {user && (
          <div className={styles.actionRow}>
            <div>
              <p className={m.rowLabel}>{t('profiles.appPassword')}</p>
              <p className={m.rowHint} role="status">
                {appReset || t('profiles.appPasswordHint')}
              </p>
            </div>
            <Button
              size="small"
              variant="secondary"
              disabled={busy}
              onClick={() => {
                setError('')
                resetAppPassword(user.id).then(
                  () => setAppReset(t('profiles.appPasswordReset')),
                  (e: unknown) => setError(e instanceof ApiError ? e.message : t('profiles.appPasswordResetFailed')),
                )
              }}
            >
              {t('profiles.appPasswordResetAction')}
            </Button>
          </div>
        )}
        {error && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}
      </div>
    </Sheet>
  )
}
