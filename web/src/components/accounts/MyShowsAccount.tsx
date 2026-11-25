// MyShows' card on «Аккаунты и синхронизация» (pages/AccountsSettings.tsx):
// link by login (the list is read from the public profile), the sync status,
// «Синхронизировать» and «Отвязать». Read only: nothing is sent back to
// MyShows. Not for a kid; absent when the server does not offer MyShows.
import { useEffect, useState } from 'react'
import { errorText, linkMyShows, syncAccount, unlinkMyShows } from '../../api/client'
import type { MyShowsState } from '../../api/types'
import { useT } from '../../i18n'
import { loadMyShows, setMyShowsState } from '../../state/accounts'
import { ago } from '../../state/format'
import { useSession } from '../../state/session'
import { InlineForm } from '../../ui/InlineForm'
import { GroupedSection, ListRow } from '../../ui/List'
import { AccountRow } from './AccountRow'
import { useSyncPolling } from './useSyncPolling'

// a read of a long list takes minutes: ask again, ever less often
const POLL_MS = 4000
const POLL_MAX_MS = 30000

export function MyShowsAccount() {
  const t = useT()
  const { user } = useSession()
  const kid = !user || user.isKid
  const [s, setS] = useState<MyShowsState | null>(null)
  const [nick, setNick] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (kid) return
    let live = true
    loadMyShows().then((a) => live && setS(a))
    return () => {
      live = false
    }
  }, [kid, user?.id])

  const apply = (n: MyShowsState) => {
    setMyShowsState(n)
    setS(n)
  }
  // a sync runs on the server (the first read after linking takes minutes for a long list,
  // «Синхронизировать», the hourly one): ask, with growing pauses, until it ends
  useSyncPolling(!!s?.syncing, (a) => a.myshows, apply, POLL_MS, POLL_MAX_MS)

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
      apply(await linkMyShows(nick.trim()))
      setNick('')
    })
  const sync = () => run(async () => apply((await syncAccount('myshows')).myshows as MyShowsState))
  const unlink = () =>
    run(async () => {
      await unlinkMyShows()
      apply({ ...s, linked: false, nickname: '', status: 'ok', lastError: '', lastSync: null, syncing: false, unmapped: 0 })
    })

  return (
    <GroupedSection title={t('acct.myshows.title')} footer={error || undefined}>
      <AccountRow linked={s.linked} name={s.nickname} direction={t('acct.dir.myshows')} status={statusLine(s, t)} syncing={s.syncing} busy={busy} onSync={() => void sync()} onUnlink={() => void unlink()} />
      {!s.linked ? (
        <ListRow wide label={<span className="visually-hidden">{t('acct.linkTitle')}</span>} hint={t('acct.myshows.hint')}>
          <InlineForm
            field={{ label: t('acct.myshows.nick'), value: nick, onChange: (e) => setNick(e.target.value), autoComplete: 'off', autoCapitalize: 'none', spellCheck: false }}
            submit={t('acct.link')}
            loading={busy}
            onSubmit={() => void link()}
          />
        </ListRow>
      ) : (
        s.syncing && !s.lastSync && <ListRow label={t('acct.myshows.firstRead')} />
      )}
    </GroupedSection>
  )
}

type T = ReturnType<typeof useT>

function statusLine(s: MyShowsState, t: T): string {
  switch (s.status) {
    case 'private':
      return t('acct.myshows.status.private')
    case 'not_found':
      return t('acct.myshows.status.notFound')
    case 'error':
      return t('acct.status.error', { error: s.lastError })
    default:
      if (!s.lastSync) return t('acct.status.never')
      return s.unmapped > 0 ? t('acct.status.okUnmapped', { ago: ago(s.lastSync), n: s.unmapped }) : t('acct.status.ok', { ago: ago(s.lastSync) })
  }
}
