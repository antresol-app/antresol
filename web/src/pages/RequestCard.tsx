// A kid's request as a card: the title's cover, kind and
// year, title and artist or author, a line under it (who asked, when) and
// the controls of the screen it is on — «Мои просьбы» shows its state,
// «Управление → Просьбы» «Одобрить» and «Отказать».
import { useState, type ReactNode } from 'react'
import { Link } from 'react-router'
import { imageUrl } from '../api/client'
import type { KidRequest } from '../api/types'
import { Icon } from '../components/Icon'
import { titlePath } from '../components/RequestButton'
import { useT } from '../i18n'
import { KIND_ICON, kindLabel } from './Search'
import styles from './Search.module.css'

export function RequestCard({ request: r, line, children }: { request: KidRequest; line?: string; children?: ReactNode }) {
  const t = useT()
  const [failed, setFailed] = useState(false)
  const poster = imageUrl(r.poster, 240)
  if (r.hidden) r = { ...r, title: t('requests.hidden') }
  const video = (r.kind === 'movie' || r.kind === 'series') && !r.hidden
  // a hidden one (above the kid's limit now) has no page to open, nor has a title in the core build
  const page = video && !__LIBRARY_EDITION__
  const art =
    poster && !failed ? (
      <img src={poster} alt="" loading="lazy" onError={() => setFailed(true)} />
    ) : (
      <Icon name={r.hidden ? 'lock' : video ? (r.kind === 'series' ? 'tv' : 'film') : (KIND_ICON[r.kind] ?? 'book')} size={28} />
    )
  return (
    <li className={styles.candidate}>
      {page ? (
        <Link to={titlePath(r)} className={styles.poster} aria-label={r.title}>
          {art}
        </Link>
      ) : (
        <span className={`${styles.poster} ${r.kind === 'music' ? styles.square : ''}`}>{art}</span>
      )}
      <div className={styles.info}>
        <p className={styles.kicker}>{[kindLabel(r.kind), r.year || '', r.rating].filter(Boolean).join(' · ')}</p>
        {page ? (
          <Link to={titlePath(r)} className={styles.name}>
            {r.title}
          </Link>
        ) : (
          <p className={styles.name}>{r.title}</p>
        )}
        {r.subtitle && <p className={styles.byline}>{r.subtitle}</p>}
        {line && <p className={styles.matched}>{line}</p>}
      </div>
      <div className={styles.footer}>{children}</div>
    </li>
  )
}
