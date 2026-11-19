// «Ключи API и ИИ» of the signed-in profile's settings (/settings/api-keys,
// the row in «Приложения и устройства» of pages/ProfileSettings.tsx): the
// profile's personal API keys for AI assistants (the MCP server at /mcp)
// and scripts (the /api routes). A key is created with a name and a scope
// and shown once, with the ready-to-copy ways to connect it; the list shows
// when each was last used; a key is revoked here. The last changes made
// with the keys are listed below. Not for a kid; an admin's page is drawn in
// «Управление»'s frame like their other own settings.
import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { Navigate, NavLink } from 'react-router'
import { createApiKey, errorText, getApiKeyLog, getApiKeys, revokeApiKey } from '../api/client'
import type { ApiKey, ApiKeyEvent, ApiKeyScope, ApiKeysState } from '../api/types'
import { APP_NAME } from '../brand'
import { CopyRow } from '../components/AppPasswordSheet'
import { Icon } from '../components/Icon'
import { locale, t as tr, useT, type Key } from '../i18n'
import { useSession } from '../state/session'
import { useIsCompact } from '../state/useMediaQuery'
import { Button } from '../ui/Button'
import { GroupedSection, ListRow } from '../ui/List'
import { Select } from '../ui/Select'
import { Sheet } from '../ui/Sheet'
import { TextField } from '../ui/TextField'
import { ManageFrame } from './manage/Manage'
import m from './manage/Manage.module.css'
import page from './ProfileSettings.module.css'
import styles from './ApiKeysSettings.module.css'

type State = { status: 'loading' } | { status: 'error'; message: string } | ({ status: 'ready' } & ApiKeysState)

const errText = (e: unknown) => errorText(e, tr('comp.serverUnreachable'))

const dateLabel = (iso: string | null | undefined) => {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleDateString(locale(), { day: 'numeric', month: 'long' })
}

const timeLabel = (iso: string) => {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString(locale(), { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' })
}

const scopeLabel = (s: ApiKeyScope) => tr(`apikeys.scope.${s}` as Key)

/** The MCP server's address: the server's public address when it has one, else this page's. */
export const mcpAddress = (publicUrl: string, mcpPath: string) => `${(publicUrl || window.location.origin).replace(/\/+$/, '')}${mcpPath || '/mcp'}`

/** The ways to connect a key: Claude Code's command, Claude Desktop's config, any MCP client's address and header. */
export function McpSnippets({ address, apiKey }: { address: string; apiKey: string }) {
  const t = useT()
  const header = `Authorization: Bearer ${apiKey}`
  const command = `claude mcp add --transport http antresol ${address} --header "${header}"`
  const config = JSON.stringify({ mcpServers: { antresol: { command: 'npx', args: ['-y', 'mcp-remote', address, '--header', header] } } })
  return (
    <div className={styles.snippets}>
      <p className={styles.caption}>{t('apikeys.connect.claudeCode')}</p>
      <CopyRow label={t('apikeys.connect.command')} value={command} />
      <p className={styles.caption}>{t('apikeys.connect.desktop')}</p>
      <CopyRow label={t('apikeys.connect.config')} value={config} />
      <p className={styles.caption}>{t('apikeys.connect.other')}</p>
      <CopyRow label={t('apikeys.connect.address')} value={address} />
      <CopyRow label={t('apikeys.connect.header')} value={header} />
    </div>
  )
}

export function ApiKeysSettings() {
  const t = useT()
  const { user } = useSession()
  const compact = useIsCompact()
  const [state, setState] = useState<State>({ status: 'loading' })
  const [log, setLog] = useState<ApiKeyEvent[] | null>(null)
  const [creating, setCreating] = useState(false)
  const [shown, setShown] = useState<{ key: string; info: ApiKey } | null>(null)
  const [revoking, setRevoking] = useState<ApiKey | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const adult = Boolean(user && !user.isKid)
  const load = useCallback(() => {
    let live = true
    setState({ status: 'loading' })
    getApiKeys()
      .then((r) => live && setState({ status: 'ready', ...r }))
      .catch((e: unknown) => live && setState({ status: 'error', message: errText(e) }))
    getApiKeyLog()
      .then((r) => live && setLog(r.events))
      .catch(() => live && setLog(null))
    return () => {
      live = false
    }
  }, [])
  useEffect(() => {
    if (!adult) return
    return load()
  }, [adult, load])

  useEffect(() => {
    document.title = `${t('apikeys.title')} — ${APP_NAME}`
  }, [t])

  if (!user) return null
  if (user.isKid) return <Navigate to="/settings" replace />

  const ready = state.status === 'ready' ? state : null
  const address = ready ? mcpAddress(ready.publicUrl, ready.mcpPath) : mcpAddress('', '/mcp')

  const revoke = (k: ApiKey) => {
    setRevoking(null)
    setBusy(true)
    setError('')
    revokeApiKey(k.id)
      .then(load)
      .catch((e: unknown) => setError(errText(e)))
      .finally(() => setBusy(false))
  }

  const Frame = user.isAdmin ? AdminFrame : PlainFrame
  return (
    <Frame>
      <section aria-labelledby="apikeys-title" className={page.column}>
        {(compact || !user.isAdmin) && (
          <NavLink to="/settings" className={m.back}>
            <Icon name="chevronLeft" size={18} strokeWidth={2.4} />
            {t('common.settings')}
          </NavLink>
        )}
        <header className={m.header}>
          <div>
            <h1 id="apikeys-title" className={m.title}>
              {t('apikeys.title')}
            </h1>
            <p className={m.subtitle}>{t('apikeys.intro', { app: APP_NAME })}</p>
          </div>
          {ready && (
            <div className={m.headerActions}>
              <Button icon="plus" disabled={busy} onClick={() => setCreating(true)}>
                {t('apikeys.create')}
              </Button>
            </div>
          )}
        </header>

        {state.status === 'loading' && (
          <div className={m.spinnerRow} role="status">
            <span className={m.spinner} aria-hidden /> {t('common.loading')}
          </div>
        )}
        {state.status === 'error' && (
          <div className={styles.retry} role="alert">
            <span className={styles.error}>{state.message}</span>
            <Button variant="secondary" size="small" onClick={load}>
              {t('common.retry')}
            </Button>
          </div>
        )}

        {ready && (
          <>
            <GroupedSection title={t('apikeys.keys')}>
              {ready.keys.length === 0 && <ListRow label={<span className={styles.muted}>{t('apikeys.none')}</span>} />}
              {ready.keys.map((k) => (
                <ListRow
                  key={k.id}
                  label={
                    <span className={styles.name}>
                      {k.name}
                      <span className={styles.hint} aria-hidden>
                        ••••{k.hint}
                      </span>
                    </span>
                  }
                  hint={[
                    scopeLabel(k.scope),
                    t('apikeys.created', { date: dateLabel(k.createdAt) }),
                    k.lastUsedAt ? t('apikeys.lastUsed', { date: dateLabel(k.lastUsedAt) }) + (k.lastUsedFrom ? ` (${k.lastUsedFrom})` : '') : t('apikeys.neverUsed'),
                  ].join(' · ')}
                >
                  <Button size="small" variant="destructive" disabled={busy} aria-label={`${t('apikeys.revoke')}: ${k.name}`} onClick={() => setRevoking(k)}>
                    {t('apikeys.revoke')}
                  </Button>
                </ListRow>
              ))}
            </GroupedSection>
            {error && (
              <p className={styles.error} role="alert">
                {error}
              </p>
            )}

            <GroupedSection title={t('apikeys.connect')} footer={t('apikeys.connect.footer', { placeholder: t('apikeys.connect.yourKey') })}>
              <div className={styles.panel}>
                <McpSnippets address={address} apiKey={t('apikeys.connect.yourKey')} />
              </div>
            </GroupedSection>

            {log && (
              <GroupedSection title={t('apikeys.log')}>
                {log.length === 0 && <ListRow label={<span className={styles.muted}>{t('apikeys.log.none')}</span>} />}
                {log.map((e, i) => (
                  <ListRow
                    key={`${e.at}-${i}`}
                    label={
                      <span className={styles.event}>
                        {e.method} {e.path}
                      </span>
                    }
                    hint={[timeLabel(e.at), e.keyName, e.via ?? '', e.status === 403 ? t('apikeys.log.refused') : String(e.status)].filter(Boolean).join(' · ')}
                  />
                ))}
              </GroupedSection>
            )}
          </>
        )}
      </section>

      {creating && ready && (
        <CreateKeySheet
          canAdmin={ready.canAdmin}
          onClose={() => setCreating(false)}
          onCreated={(r) => {
            setCreating(false)
            setShown(r)
            load()
          }}
        />
      )}
      {shown && (
        <Sheet
          title={t('apikeys.shownTitle', { name: shown.info.name })}
          role="dialog"
          size="wide"
          message={t('apikeys.saveIt')}
          onCancel={() => setShown(null)}
          closeButton={false}
          actions={[{ label: t('common.done'), kind: 'primary', run: () => setShown(null) }]}
        >
          <div className={styles.body} tabIndex={-1} data-autofocus>
            <CopyRow label={t('apikeys.key')} value={shown.key} field />
            <McpSnippets address={address} apiKey={shown.key} />
          </div>
        </Sheet>
      )}
      {revoking && (
        <Sheet
          title={t('apikeys.revokeTitle', { name: revoking.name })}
          message={t('apikeys.revokeMessage')}
          onCancel={() => setRevoking(null)}
          actions={[
            { label: t('common.cancelAction'), kind: 'cancel', run: () => setRevoking(null) },
            { label: t('apikeys.revoke'), kind: 'destructive', run: () => revoke(revoking) },
          ]}
        />
      )}
    </Frame>
  )
}

function CreateKeySheet({ canAdmin, onClose, onCreated }: { canAdmin: boolean; onClose: () => void; onCreated: (r: { key: string; info: ApiKey }) => void }) {
  const t = useT()
  const [name, setName] = useState('')
  const [scope, setScope] = useState<ApiKeyScope>('read')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const scopes: ApiKeyScope[] = canAdmin ? ['read', 'act', 'admin'] : ['read', 'act']
  const create = () => {
    if (!name.trim() || busy) return
    setBusy(true)
    setError('')
    createApiKey(name.trim(), scope)
      .then(onCreated)
      .catch((e: unknown) => setError(errText(e)))
      .finally(() => setBusy(false))
  }
  return (
    <Sheet
      title={t('apikeys.createTitle')}
      role="dialog"
      size="wide"
      busy={busy}
      onCancel={onClose}
      actions={[
        { label: t('common.cancelAction'), kind: 'cancel', run: onClose },
        { label: t('common.create'), kind: 'primary', disabled: !name.trim() || busy, run: create },
      ]}
    >
      <form
        className={styles.body}
        onSubmit={(e) => {
          e.preventDefault()
          create()
        }}
      >
        <TextField label={t('apikeys.name')} placeholder={t('apikeys.namePlaceholder')} value={name} maxLength={80} autoFocus onChange={(e) => setName(e.currentTarget.value)} />
        <div className={styles.scopeRow}>
          <span className={styles.scopeLabel}>{t('apikeys.scope')}</span>
          <Select
            label={t('apikeys.scope')}
            placement="bottom-end"
            value={scope}
            options={scopes.map((s) => ({ value: s, label: scopeLabel(s), detail: t(`apikeys.scope.${s}.hint` as Key) }))}
            onChange={(v: ApiKeyScope) => setScope(v)}
          />
        </div>
        <p className={styles.note}>{t(`apikeys.scope.${scope}.hint` as Key)}</p>
        {error && (
          <p className={styles.error} role="alert">
            {error}
          </p>
        )}
      </form>
    </Sheet>
  )
}

const AdminFrame = ({ children }: { children: ReactNode }) => <ManageFrame nested>{children}</ManageFrame>
const PlainFrame = ({ children }: { children: ReactNode }) => <div className={m.page}>{children}</div>

export default ApiKeysSettings
