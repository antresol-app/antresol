// «Отмечать и на Shikimori?»: the one-time question after a profile's first
// mark change (state/writePrompt.ts decides when). «Да» goes to Shikimori's
// sign-in in this tab, «Нет» declines for good (it stays in the profile's
// settings).
import { useState } from 'react'
import { setShikimoriWrite } from '../../api/client'
import { useT } from '../../i18n'
import { setAccountsState, loadAccounts } from '../../state/accounts'
import { closeWritePrompt, useWritePromptOpen } from '../../state/writePrompt'
import { Sheet } from '../../ui/Sheet'

export function WritePrompt() {
  const t = useT()
  const open = useWritePromptOpen()
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)
  if (!open) return null

  const answer = async (enable: boolean) => {
    setBusy(true)
    setFailed(false)
    try {
      const r = await setShikimoriWrite(enable)
      if (enable) {
        const url = r && 'authorizeUrl' in r ? r.authorizeUrl : undefined
        if (url) window.location.assign(url)
        return
      }
      const s = await loadAccounts()
      if (s) setAccountsState({ ...s, writeState: 'declined' })
      closeWritePrompt()
    } catch {
      setFailed(true)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Sheet
      title={t('acct.prompt.title')}
      message={failed ? t('acct.failed') : t('acct.prompt.message')}
      busy={busy}
      onCancel={closeWritePrompt}
      actions={[
        { label: t('acct.no'), kind: 'cancel', run: () => void answer(false) },
        { label: t('acct.yes'), kind: 'primary', run: () => void answer(true) },
      ]}
    />
  )
}
