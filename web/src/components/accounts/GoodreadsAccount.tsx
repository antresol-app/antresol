// Goodreads' card on «Аккаунты и синхронизация» (pages/AccountsSettings.tsx).
// Goodreads has no open API, so there are two ways in, both read only: link
// the profile by its address (the public shelves are read, up to 100 newest
// books of each) and upload the library export (CSV: the whole history, no
// link needed). The books go onto «Читаю сейчас», «Хочу прочитать» and
// «Прочитано». Not for a kid; absent when the server does not offer Goodreads.
import { useEffect, useRef, useState } from 'react'
import { ApiError, errorText, importGoodreads, linkGoodreads, syncAccount, unlinkGoodreads } from '../../api/client'
import type { GoodreadsState } from '../../api/types'
import { useT } from '../../i18n'
import { loadGoodreads, setGoodreadsState } from '../../state/accounts'
import { ago } from '../../state/format'
import { useSession } from '../../state/session'
import { Button } from '../../ui/Button'
import { FileInput } from '../../ui/FileInput'
import { InlineForm } from '../../ui/InlineForm'
import { GroupedSection, ListRow } from '../../ui/List'
import { AccountRow } from './AccountRow'
import { useSyncPolling } from './useSyncPolling'

// the first read of the shelves takes seconds: ask again, ever less often
const POLL_MS = 2000
const POLL_MAX_MS = 15000
// the server refuses a bigger body
const MAX_FILE = 24 * 1024 * 1024

export function GoodreadsAccount() {
  const t = useT()
  const { user } = useSession()
  const kid = !user || user.isKid
  const [s, setS] = useState<GoodreadsState | null>(null)
  const [profile, setProfile] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [imported, setImported] = useState('')
  const picker = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (kid) return
    let live = true
    loadGoodreads().then((a) => live && setS(a))
    return () => {
      live = false
    }
  }, [kid, user?.id])

  const apply = (n: GoodreadsState) => {
    setGoodreadsState(n)
    setS(n)
  }
  // a sync runs on the server (the first read after linking, «Синхронизировать», the
  // periodic one): ask, with growing pauses, until it ends
  useSyncPolling(!!s?.syncing, (a) => a.goodreads, apply, POLL_MS, POLL_MAX_MS)

  if (kid || !s) return null
  const run = async (f: () => Promise<void>) => {
    setBusy(true)
    setError('')
    try {
      await f()
    } catch (e) {
      // a proxy's refusal of a big file has no message of ours
      setError(e instanceof ApiError && e.status === 413 ? t('acct.goodreads.tooLarge') : errorText(e, t('acct.failed')))
    } finally {
      setBusy(false)
    }
  }

  const link = () =>
    run(async () => {
      apply(await linkGoodreads(profile.trim()))
      setProfile('')
    })
  const sync = () => run(async () => apply((await syncAccount('goodreads')).goodreads as GoodreadsState))
  const forget = () =>
    run(async () => {
      await unlinkGoodreads()
      setImported('')
      apply({ ...s, linked: false, nickname: '', status: 'ok', lastError: '', lastSync: null, syncing: false, total: 0, read: 0, reading: 0, toRead: 0, inLibrary: 0 })
    })
  const pick = (file: File | undefined) => {
    if (!file) return
    setImported('')
    if (file.size > MAX_FILE) {
      setError(t('acct.goodreads.tooLarge'))
      return
    }
    void run(async () => {
      const n = await importGoodreads(await file.text())
      apply(n)
      setImported(t('acct.goodreads.imported', { count: n.total, inLibrary: n.inLibrary }))
    })
  }

  return (
    <GroupedSection title={t('acct.goodreads.title')} footer={error || undefined}>
      <AccountRow linked={s.linked} name={s.nickname} direction={t('acct.dir.goodreads')} status={statusLine(s, t)} syncing={s.syncing} busy={busy} onSync={() => void sync()} onUnlink={() => void forget()} />
      {!s.linked ? (
        <ListRow wide label={<span className="visually-hidden">{t('acct.linkTitle')}</span>} hint={t('acct.goodreads.hint')}>
          <InlineForm
            field={{ label: t('acct.goodreads.profile'), value: profile, onChange: (e) => setProfile(e.target.value), autoComplete: 'off', autoCapitalize: 'none', spellCheck: false }}
            submit={t('acct.link')}
            loading={busy}
            onSubmit={() => void link()}
          />
        </ListRow>
      ) : (
        s.syncing && !s.lastSync && <ListRow label={t('acct.goodreads.firstRead')} />
      )}
      <ListRow stack label={t('acct.goodreads.file')} hint={t('acct.goodreads.file.hint')} status={imported || undefined}>
        {/* no accept: a spreadsheet app may have renamed the file, the server checks the content */}
        <FileInput
          ref={picker}
          data-testid="goodreads-file"
          onChange={(e) => {
            pick(e.target.files?.[0])
            e.target.value = ''
          }}
        />
        <Button variant="secondary" size="small" loading={busy} onClick={() => picker.current?.click()}>
          {t('acct.goodreads.pick')}
        </Button>
      </ListRow>
      {s.total > 0 && (
        <ListRow label={t('acct.goodreads.summary', { total: s.total, read: s.read, reading: s.reading, toRead: s.toRead, inLibrary: s.inLibrary })}>
          {!s.linked && (
            <Button variant="secondary" size="small" disabled={busy} onClick={() => void forget()}>
              {t('acct.goodreads.forget')}
            </Button>
          )}
        </ListRow>
      )}
    </GroupedSection>
  )
}

type T = ReturnType<typeof useT>

function statusLine(s: GoodreadsState, t: T): string {
  switch (s.status) {
    case 'not_found':
      return t('acct.goodreads.status.notFound')
    case 'error':
      return t('acct.status.error', { error: s.lastError })
    default:
      if (!s.lastSync) return t('acct.status.never')
      return s.total === 0 ? t('acct.goodreads.empty') : t('acct.status.ok', { ago: ago(s.lastSync) })
  }
}
