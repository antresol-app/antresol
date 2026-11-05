// «Мои просьбы» (a kid profile, from the profile menu): what this profile
// asked for and what the parents answered — «Ждёт ответа», «Одобрено —
// скоро появится», «Уже в медиатеке» (a link to it), «Родитель отказал» with
// the reason. Updates live (request.changed, library.updated).
import { useEffect } from 'react'
import { APP_NAME } from '../brand'
import { RequestStatus } from '../components/RequestButton'
import { Icon } from '../components/Icon'
import { ago } from '../state/format'
import { useMyRequests } from '../state/kidRequests'
import { useSession } from '../state/session'
import { useT } from '../i18n'
import { RequestCard } from './RequestCard'
import m from './manage/Manage.module.css'
import s from './Search.module.css'

export function MyRequests() {
  const t = useT()
  const { user } = useSession()
  const { requests } = useMyRequests(user?.id ?? 0)
  const list = [...requests.values()].sort((a, b) => b.createdAt.localeCompare(a.createdAt) || b.id - a.id)
  useEffect(() => {
    document.title = `${t('requests.mine')} — ${APP_NAME}`
  }, [t])

  return (
    <div className={m.page}>
      <div style={{ maxWidth: 1080, margin: '0 auto' }}>
        <header className={m.header}>
          <h1 className={m.title}>{t('requests.mine')}</h1>
        </header>
        {list.length === 0 ? (
          <div className={m.empty}>
            <Icon name="send" size={40} strokeWidth={1.4} />
            <p>{t('requests.mineEmpty')}</p>
          </div>
        ) : (
          <ul className={s.candidates}>
            {list.map((r) => (
              <RequestCard key={r.id} request={r} line={ago(r.createdAt)}>
                <div className={s.buttons}>
                  <RequestStatus request={r} />
                </div>
              </RequestCard>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

export default MyRequests
