// «Аккаунты и синхронизация» of the signed-in profile's settings
// (/settings/accounts, the row in pages/ProfileSettings.tsx): every service
// the profile can link as a card — linked as whom (or not), what syncs which
// way in one line, when the last sync ran and how it went, and its actions
// (link, unlink, the write-back switch, the Goodreads file). What applies to
// the whole server (Shikimori's application) is the admin's, in «Управление →
// Настройки сервера → Аккаунты и синхронизация». Not for a kid; an admin's
// page is drawn in «Управление»'s frame like their other own settings.
// Shikimori's sign-in comes back here (`?account=shikimori&…`).
import { useEffect, useLayoutEffect, useState, type ReactNode } from 'react'
import { Navigate, NavLink, useSearchParams } from 'react-router'
import { APP_NAME } from '../brand'
import { GoodreadsAccount } from '../components/accounts/GoodreadsAccount'
import { MyShowsAccount } from '../components/accounts/MyShowsAccount'
import { ShikimoriAccount } from '../components/accounts/ShikimoriAccount'
import { Icon } from '../components/Icon'
import { useT, type Key } from '../i18n'
import { loadAccountsState, resetAccounts } from '../state/accounts'
import { showNotice } from '../state/notice'
import { useSession } from '../state/session'
import { useIsCompact } from '../state/useMediaQuery'
import { ManageFrame } from './manage/Manage'
import { Spinner } from '../ui/Spinner'
import m from './manage/Manage.module.css'
import styles from './ProfileSettings.module.css'

/** The reasons Shikimori's sign-in can come back with an error (the server's `error=`). */
const KNOWN_ERRORS = ['session', 'state', 'denied', 'exchange', 'account']

export function AccountsSettings() {
  const t = useT()
  const { user } = useSession()
  const compact = useIsCompact()
  const [params, setParams] = useSearchParams()
  // undefined: asking; null: this server has no accounts
  const [offered, setOffered] = useState<boolean | undefined>(undefined)

  // a fresh read: the cached link state may be a session old (a layout effect:
  // it runs before the cards' own loads start)
  useLayoutEffect(() => {
    resetAccounts()
  }, [])

  // back from Shikimori's sign-in (the server's redirect): say how it went, clear the address
  useEffect(() => {
    if (params.get('account') !== 'shikimori') return
    const error = params.get('error')
    resetAccounts() // the link's state changed on the server
    showNotice(error ? t(`acct.err.${KNOWN_ERRORS.includes(error) ? error : 'exchange'}` as Key) : t('acct.ok'), 5000)
    setParams({}, { replace: true })
  }, [params, setParams, t])

  const adult = Boolean(user && !user.isKid)
  useEffect(() => {
    if (!adult) return
    let live = true
    void loadAccountsState().then((a) => live && setOffered(Boolean(a?.shikimori)))
    return () => {
      live = false
    }
  }, [adult, user?.id])

  useEffect(() => {
    document.title = `${t('acct.section')} — ${APP_NAME}`
  }, [t])

  if (!user) return null
  // a kid has no accounts to link
  if (user.isKid) return <Navigate to="/settings" replace />

  const Frame = user.isAdmin ? AdminFrame : PlainFrame
  return (
    <Frame>
      <section aria-labelledby="accounts-title" className={styles.column}>
        {/* an admin's wide window has the profile's row in the sidebar; everyone else needs the way back */}
        {(compact || !user.isAdmin) && (
          <NavLink to="/settings" className={m.back}>
            <Icon name="chevronLeft" size={18} strokeWidth={2.4} />
            {t('common.settings')}
          </NavLink>
        )}
        <header className={m.header}>
          <div>
            <h1 id="accounts-title" className={m.title}>
              {t('acct.section')}
            </h1>
            <p className={m.subtitle}>{t('acct.page.intro')}</p>
          </div>
        </header>
        {offered === undefined ? (
          <div className={m.spinnerRow} role="status">
            <Spinner track className={m.spinner} /> {t('common.loading')}
          </div>
        ) : offered === false ? (
          <div className={m.empty}>
            <Icon name="info" size={40} strokeWidth={1.4} />
            <h3>{t('acct.empty.title')}</h3>
            <p>{t('acct.empty.text')}</p>
          </div>
        ) : (
          <>
            <ShikimoriAccount />
            <MyShowsAccount />
            <GoodreadsAccount />
          </>
        )}
      </section>
    </Frame>
  )
}

const AdminFrame = ({ children }: { children: ReactNode }) => <ManageFrame nested>{children}</ManageFrame>
const PlainFrame = ({ children }: { children: ReactNode }) => <div className={m.page}>{children}</div>

export default AccountsSettings
