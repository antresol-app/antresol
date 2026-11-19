// Shikimori's card on «Аккаунты и синхронизация» (pages/AccountsSettings.tsx):
// link by nickname (the list is read from the public profile), the sync
// status, «Отмечать и на Shikimori» (needs a sign-in there: opens it in this
// tab), «Синхронизировать» and «Отвязать». Not for a kid; absent when the
// server has no accounts.

import { useEffect, useState } from 'react'
import { errorText, linkShikimori, setShikimoriWrite, syncAccount, unlinkShikimori } from '../../api/client'
import type { ShikimoriState } from '../../api/types'
import { useT, type Key } from '../../i18n'
import { loadAccounts, setAccountsState } from '../../state/accounts'
import { ago } from '../../state/format'
import { useSession } from '../../state/session'
import { InlineForm } from '../../ui/InlineForm'
import { GroupedSection, ListRow } from '../../ui/List'
import { Toggle } from '../../ui/Toggle'
import { AccountRow } from './AccountRow'
import { useSyncPolling } from './useSyncPolling'

// a pull of the list takes seconds: ask again, ever less often
const POLL_MS = 2000
const POLL_MAX_MS = 15000

export function ShikimoriAccount() {
  const t = useT()
  const { user } = useSession()
  const kid = !user || user.isKid
  const [s, setS] = useState<ShikimoriState | null>(null)
  const [nick, setNick] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (kid) return
    let live = true
    loadAccounts().then((a) => live && setS(a))
    return () => {
      live = false
    }
  }, [kid, user?.id])

  const apply = (n: ShikimoriState) => {
    setAccountsState(n)
    setS(n)
  }
  // a sync runs on the server (after linking, «Синхронизировать», the periodic one):
  // ask until it ends, then the status line tells how it went
  useSyncPolling(!!s?.syncing, (a) => a.shikimori, apply, POLL_MS, POLL_MAX_MS)

  if (kid || !s) return null
  const run = async (f: () => Promise<void>) => {
    setBusy(true)
    setError('')
    try {
      await f()
    } catch (e) {
      setError(errorText(e, t('acct.failed')))
    } finally {
      setBusy(false)
    }
  }

  const link = () =>
    run(async () => {
      apply(await linkShikimori(nick.trim()))
      setNick('')
    })
  const sync = () => run(async () => apply((await syncAccount('shikimori')).shikimori))
  const unlink = () =>
    run(async () => {
      await unlinkShikimori()
      apply({ ...s, linked: false, nickname: '', status: 'ok', lastError: '', lastSync: null, syncing: false, writeState: 'unasked', unmapped: 0 })
    })
  const write = (on: boolean) =>
    run(async () => {
      const r = await setShikimoriWrite(on)
      if (on) {
        const url = r && 'authorizeUrl' in r ? r.authorizeUrl : undefined
        if (url) window.location.assign(url)
        return
      }
      apply({ ...s, writeState: 'declined' })
    })

  return (
    <GroupedSection title={t('acct.shikimori.title')} footer={error || undefined}>
      <AccountRow linked={s.linked} name={s.nickname} direction={t(direction(s))} status={statusLine(s, t)} syncing={s.syncing} busy={busy} onSync={() => void sync()} onUnlink={() => void unlink()} />
      {!s.linked ? (
        <ListRow wide label={t('acct.linkTitle')} hint={t('acct.hint')}>
          <InlineForm
            field={{ label: t('acct.nick'), value: nick, onChange: (e) => setNick(e.target.value), autoComplete: 'off', autoCapitalize: 'none', spellCheck: false }}
            submit={t('acct.link')}
            loading={busy}
            onSubmit={() => void link()}
          />
        </ListRow>
      ) : (
        <ListRow label={t('acct.write')} hint={s.canWrite || s.writeState === 'enabled' ? t('acct.write.hint') : t('acct.write.unavailable')}>
          <Toggle label={t('acct.write')} checked={s.writeState === 'enabled'} disabled={busy || (!s.canWrite && s.writeState !== 'enabled')} onChange={(on) => void write(on)} />
        </ListRow>
      )}
    </GroupedSection>
  )
}

type T = ReturnType<typeof useT>

/** What flows which way: both ways only once the profile has allowed marks to go to Shikimori. */
function direction(s: ShikimoriState): Key {
  if (!s.linked) return 'acct.dir.shikimori.setup'
  return s.writeState === 'enabled' ? 'acct.dir.shikimori.both' : 'acct.dir.shikimori.read'
}

function statusLine(s: ShikimoriState, t: T): string {
  switch (s.status) {
    case 'private':
      return t('acct.status.private')
    case 'not_found':
      return t('acct.status.notFound')
    case 'relogin':
      return t('acct.status.relogin')
    case 'error':
      return t('acct.status.error', { error: s.lastError })
    default:
      if (!s.lastSync) return t('acct.status.never')
      return s.unmapped > 0 ? t('acct.status.okUnmapped', { ago: ago(s.lastSync), n: s.unmapped }) : t('acct.status.ok', { ago: ago(s.lastSync) })
  }
}
