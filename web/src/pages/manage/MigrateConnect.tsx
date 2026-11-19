// Under the Jellyfin, Kavita and Audiobookshelf cards of «Переезд»: reach the
// service over the network — its address and a key (Jellyfin: or a user name
// and a password). This is the first way offered: the form is open on a card
// whose database file is not mounted; a card whose file is mounted reads the
// file and offers «Подключить по сети» in one line. «Подключить» checks them
// on the server, which keeps them in memory for the migration and counts what
// it found; the card then counts as found, and the usual «Проверить» and
// «Перенести» follow. The key and the password are never answered back: a
// kept one shows as «Сохранён», and a blank field keeps it.
import { useState, type FormEvent } from 'react'
import { migrateConnect, migrateForget, errorText as apiErrorText } from '../../api/client'
import type { MigrateConnect, MigrateConnectRequest, MigrateSource } from '../../api/types'
import { Button } from '../../ui/Button'
import { SegmentedControl } from '../../ui/SegmentedControl'
import { TextField } from '../../ui/TextField'
import { t as tr, useT, type Key } from '../../i18n'
import s from './MigrateConnect.module.css'

/** Where each service usually listens: the address field's example. */
const EXAMPLE: Record<string, string> = {
  jellyfin: 'http://jellyfin:8096',
  kavita: 'http://kavita:5000',
  audiobookshelf: 'http://audiobookshelf:13378',
}

/** Where the key is found in each service. */
const KEY_HINT: Record<string, Key> = {
  jellyfin: 'migrate.connect.hint.jellyfin',
  kavita: 'migrate.connect.hint.kavita',
  audiobookshelf: 'migrate.connect.hint.audiobookshelf',
}

type By = 'key' | 'login'

/** The address as the server keeps it: trimmed, without a trailing slash. */
export const cleanAddress = (url: string) => url.trim().replace(/\/+$/, '')

/** What the person has typed. */
export interface ConnectFields {
  by: By
  url: string
  apiKey: string
  username: string
  password: string
}

/** The kept key (or password) still applies: same address (and, for a login, the same user name). The server applies the same rule. */
export function keepsSecret(c: MigrateConnect, f: ConnectFields): boolean {
  if (!c.hasSecret || cleanAddress(f.url) !== c.url) return false
  return f.by === 'login' ? f.username.trim() === (c.username ?? '') : !c.username
}

/** «Подключить» may be pressed: an address and a key (or a user name and a password) typed or kept. */
export function canSubmit(c: MigrateConnect, f: ConnectFields): boolean {
  if (cleanAddress(f.url) === '') return false
  if (f.by === 'login') return f.username.trim() !== '' && (f.password !== '' || keepsSecret(c, f))
  return f.apiKey.trim() !== '' || keepsSecret(c, f)
}

/** The request: only the fields of the way chosen. */
export function requestOf(f: ConnectFields): MigrateConnectRequest {
  const url = f.url.trim()
  return f.by === 'login' ? { url, username: f.username.trim(), password: f.password } : { url, apiKey: f.apiKey.trim() }
}

export function MigrateConnectForm({ source, locked, onChanged }: { source: MigrateSource; locked: boolean; onChanged: (connected: boolean) => void }) {
  const t = useT()
  const connect = source.connect
  const [open, setOpen] = useState(false)
  const [by, setBy] = useState<By>(connect?.username ? 'login' : 'key')
  const [url, setUrl] = useState(connect?.url ?? '')
  const [apiKey, setApiKey] = useState('')
  const [username, setUsername] = useState(connect?.username ?? '')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  if (!connect) return null

  const fields: ConnectFields = { by, url, apiKey, username, password }
  const disabled = locked || busy
  const kept = keepsSecret(connect, fields)
  const failed = (e: unknown) => setError(apiErrorText(e, tr('migrate.failedRetry')))

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (disabled || !canSubmit(connect, fields)) return
    setBusy(true)
    setError('')
    try {
      await migrateConnect(source.id, requestOf(fields))
      setOpen(false)
      setApiKey('')
      setPassword('')
      onChanged(true)
    } catch (err) {
      failed(err)
    } finally {
      setBusy(false)
    }
  }

  const forget = async () => {
    setBusy(true)
    setError('')
    try {
      await migrateForget(source.id)
      onChanged(false)
    } catch (err) {
      failed(err)
    } finally {
      setBusy(false)
    }
  }

  const errorLine = error && (
    <p className={s.error} role="alert">
      {error}
    </p>
  )

  if (connect.connected && !open) {
    return (
      <div className={s.line} data-testid="migrate-connected">
        <span className={s.text}>{t('migrate.connect.connected', { url: connect.url })}</span>
        <span className={s.actions}>
          <Button size="small" variant="secondary" disabled={disabled} onClick={() => setOpen(true)}>
            {t('migrate.connect.change')}
          </Button>
          <Button size="small" variant="secondary" disabled={disabled} loading={busy} onClick={() => void forget()}>
            {t('migrate.connect.forget')}
          </Button>
        </span>
        {errorLine}
      </div>
    )
  }

  // the file is mounted and read: the network is one line away
  if (connect.mounted && !connect.connected && !open) {
    return (
      <div className={s.line} data-testid="migrate-connect-offer">
        <span className={s.text}>{t('migrate.connect.mounted')}</span>
        <span className={s.actions}>
          <Button size="small" variant="secondary" icon="plug" disabled={disabled} onClick={() => setOpen(true)}>
            {t('migrate.connect.open')}
          </Button>
        </span>
      </div>
    )
  }

  const keptHint = kept ? t('migrate.connect.kept') : undefined
  const keyLabel = source.id === 'audiobookshelf' ? t('migrate.connect.apiToken') : t('migrate.connect.apiKey')
  return (
    <form className={s.form} onSubmit={(e) => void submit(e)} data-testid="migrate-connect" aria-label={t('migrate.connect.aria', { name: source.name })}>
      {connect.kind === 'apikeyOrLogin' && (
        <SegmentedControl
          size="small"
          label={t('migrate.connect.by')}
          segments={[
            { value: 'key', label: t('migrate.connect.byKey') },
            { value: 'login', label: t('migrate.connect.byLogin') },
          ]}
          value={by}
          onChange={setBy}
        />
      )}
      <TextField
        label={t('migrate.connect.url')}
        type="url"
        inputMode="url"
        value={url}
        placeholder={EXAMPLE[source.id]}
        onChange={(e) => setUrl(e.target.value)}
        autoComplete="off"
        spellCheck={false}
        disabled={disabled}
      />
      {by === 'login' ? (
        <>
          <div className={s.pair}>
            <TextField
              label={t('migrate.connect.username')}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              disabled={disabled}
            />
            <TextField
              label={t('migrate.connect.password')}
              type="password"
              revealable
              value={password}
              placeholder={keptHint}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
              disabled={disabled}
            />
          </div>
          <p className={s.hint}>{t('migrate.connect.loginHint')}</p>
        </>
      ) : (
        <TextField
          label={keyLabel}
          type="password"
          revealable
          value={apiKey}
          placeholder={keptHint}
          hint={t(KEY_HINT[source.id] ?? 'migrate.connect.hint.jellyfin')}
          onChange={(e) => setApiKey(e.target.value)}
          autoComplete="new-password"
          spellCheck={false}
          disabled={disabled}
        />
      )}
      {errorLine}
      <div className={s.submit}>
        <Button type="submit" size="small" icon="plug" loading={busy} disabled={disabled || !canSubmit(connect, fields)}>
          {t('migrate.connect.submit')}
        </Button>
        {open && (
          <Button size="small" variant="secondary" disabled={busy} onClick={() => setOpen(false)}>
            {t('common.cancel')}
          </Button>
        )}
      </div>
    </form>
  )
}
