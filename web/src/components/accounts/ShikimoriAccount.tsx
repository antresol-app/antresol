// «Аккаунты → Shikimori» of a profile's settings: link by nickname (the
// list is read from the public profile), the sync status, «Отмечать и на
// Shikimori» (needs a sign-in there: opens it in this tab) and «Отвязать».
// Not for a kid; absent when the server has no accounts.
import { useEffect, useState } from 'react'
import { errorText, linkShikimori, setShikimoriWrite, unlinkShikimori } from '../../api/client'
import type { ShikimoriState } from '../../api/types'
import { useT } from '../../i18n'
import { loadAccounts, setAccountsState } from '../../state/accounts'
import { ago } from '../../state/format'
import { useSession } from '../../state/session'
import { Button } from '../../ui/Button'
import { InlineForm } from '../../ui/InlineForm'
import { GroupedSection, ListRow } from '../../ui/List'
import { Toggle } from '../../ui/Toggle'

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

  if (kid || !s) return null
  const apply = (n: ShikimoriState) => {
    setAccountsState(n)
    setS(n)
  }
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
  const unlink = () =>
    run(async () => {
      await unlinkShikimori()
      apply({ ...s, linked: false, nickname: '', status: 'ok', lastError: '', lastSync: null, writeState: 'unasked', unmapped: 0 })
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
    <GroupedSection title={t('acct.title')} footer={error || undefined}>
      {!s.linked ? (
        <ListRow wide label="Shikimori" hint={t('acct.hint')}>
          <InlineForm
            field={{ label: t('acct.nick'), value: nick, onChange: (e) => setNick(e.target.value), autoComplete: 'off', autoCapitalize: 'none', spellCheck: false }}
            submit={t('acct.link')}
            loading={busy}
            onSubmit={() => void link()}
          />
        </ListRow>
      ) : (
        <>
          <ListRow label={s.nickname} hint="Shikimori" status={statusLine(s, t)}>
            <Button variant="secondary" size="small" disabled={busy} onClick={() => void unlink()}>
              {t('acct.unlink')}
            </Button>
          </ListRow>
          <ListRow label={t('acct.write')} hint={s.canWrite || s.writeState === 'enabled' ? t('acct.write.hint') : t('acct.write.unavailable')}>
            <Toggle label={t('acct.write')} checked={s.writeState === 'enabled'} disabled={busy || (!s.canWrite && s.writeState !== 'enabled')} onChange={(on) => void write(on)} />
          </ListRow>
        </>
      )}
    </GroupedSection>
  )
}

type T = ReturnType<typeof useT>

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
