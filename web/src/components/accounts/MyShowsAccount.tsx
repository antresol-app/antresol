// «Аккаунты → MyShows» of a profile's settings: link by login (the list is
// read from the public profile), the sync status and «Отвязать». Read only:
// nothing is sent back to MyShows. Not for a kid; absent when the server
// does not offer MyShows.
import { useEffect, useState } from 'react'
import { errorText, getAccounts, linkMyShows, unlinkMyShows } from '../../api/client'
import type { MyShowsState } from '../../api/types'
import { useT } from '../../i18n'
import { loadMyShows, setMyShowsState } from '../../state/accounts'
import { ago } from '../../state/format'
import { useSession } from '../../state/session'
import { Button } from '../../ui/Button'
import { GroupedSection, ListRow } from '../../ui/List'
import { TextField } from '../../ui/TextField'

// a first read of a long list takes minutes: ask again, ever less often
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

  // after a link the first pull runs on the server (minutes for a long
  // list): ask again, with growing pauses, until it has stored a sync time
  const polling = !!s?.linked && !s.lastSync && s.status === 'ok'
  useEffect(() => {
    if (!polling) return
    let live = true
    let timer: ReturnType<typeof setTimeout>
    const next = (delay: number) => {
      timer = setTimeout(() => {
        getAccounts().then(
          (a) => {
            if (!live || !a.myshows) return
            setMyShowsState(a.myshows)
            setS(a.myshows)
          },
          () => {},
        ).finally(() => {
          if (live) next(Math.min(delay * 1.5, POLL_MAX_MS))
        })
      }, delay)
    }
    next(POLL_MS)
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [polling])

  if (kid || !s) return null
  const apply = (n: MyShowsState) => {
    setMyShowsState(n)
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
      apply(await linkMyShows(nick.trim()))
      setNick('')
    })
  const unlink = () =>
    run(async () => {
      await unlinkMyShows()
      apply({ ...s, linked: false, nickname: '', status: 'ok', lastError: '', lastSync: null, unmapped: 0 })
    })

  return (
    <GroupedSection title={t('acct.myshows.title')} footer={error || undefined}>
      {!s.linked ? (
        <ListRow wide label="MyShows" hint={t('acct.myshows.hint')}>
          <form
            onSubmit={(e) => {
              e.preventDefault()
              if (nick.trim()) void link()
            }}
          >
            <TextField label={t('acct.myshows.nick')} value={nick} onChange={(e) => setNick(e.target.value)} autoComplete="off" autoCapitalize="none" spellCheck={false} />
            <Button type="submit" loading={busy} disabled={!nick.trim()}>
              {t('acct.link')}
            </Button>
          </form>
        </ListRow>
      ) : (
        <>
          <ListRow label={s.nickname} hint="MyShows" status={statusLine(s, t)}>
            <Button variant="secondary" size="small" disabled={busy} onClick={() => void unlink()}>
              {t('acct.unlink')}
            </Button>
          </ListRow>
          <ListRow label={t('acct.myshows.note')} />
          {polling && <ListRow label={t('acct.myshows.firstRead')} />}
        </>
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
