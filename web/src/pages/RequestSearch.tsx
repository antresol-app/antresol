// «Найти и попросить» on «Поиск» for a kid profile while requests are open
// (core): what is not in the library yet, from GET /api/requests/search — the
// page's one filter picks the kind (movies and series, music, books,
// audiobooks, manga). Each row has «Попросить» or the request's state
// (RequestButton). Movies and series keep their poster (and, where the server
// has title pages, a link to theirs: the age rating is known there); albums
// and books get placeholder art, never an outside cover.
import { useEffect, useState } from 'react'
import { Link } from 'react-router'
import { errorText, imageUrl, searchRequests, type KidRequestAsk, type RequestSearchKind } from '../api/client'
import type { KidCandidate } from '../api/types'
import type { SearchScope } from '../app/sections'
import { Icon } from '../components/Icon'
import { RequestButton, titlePath } from '../components/RequestButton'
import { useT, type Key } from '../i18n'
import { KIND_ICON, kindLabel, useDebounced, type Found } from '../state/searchKinds'
import styles from './Search.module.css'

/** The kind of the request search a scope of the page's filter picks. */
const SCOPE_KIND: Record<SearchScope, RequestSearchKind> = { all: 'video', movies: 'video', series: 'video', music: 'music', books: 'book', audiobooks: 'audiobook', comics: 'comic' }
/** «Фильмы» / «Сериалы» narrow the video answer (no new request). */
const SCOPE_ONLY: Partial<Record<SearchScope, string>> = { movies: 'movie', series: 'series' }
/** What the block says it searches, next to its title. */
const WHERE: Record<SearchScope, Key> = {
  all: 'requests.search.video',
  movies: 'common.movies',
  series: 'common.series',
  music: 'common.music',
  books: 'common.books',
  audiobooks: 'common.audiobooks',
  comics: 'common.kind.manga',
}

const isVideo = (kind: string) => kind === 'movie' || kind === 'series'

/**
 * The kid's request search under the library results. nothing: the library
 * found nothing either, so an empty answer leaves the page's one empty state.
 */
export function RequestSearch({ q, text, scope, nothing = false }: { q: string; text: string; scope: SearchScope; nothing?: boolean }) {
  const t = useT()
  const kind = SCOPE_KIND[scope]
  const dq = useDebounced(text.trim(), 600)
  const [found, setFound] = useState<Found<KidCandidate[]>>({ status: 'idle' })

  useEffect(() => {
    if (dq.length < 2) return setFound({ status: 'idle' })
    const ac = new AbortController()
    setFound({ status: 'loading' })
    searchRequests(dq, kind, ac.signal).then(
      (items) => setFound({ status: 'ready', items: Array.isArray(items) ? items : [] }),
      (e: unknown) => !ac.signal.aborted && setFound({ status: 'error', message: errorText(e, t('requests.search.failed')) }),
    )
    return () => ac.abort()
  }, [dq, kind])

  const only = SCOPE_ONLY[scope]
  const items = found.status === 'ready' ? (only ? found.items.filter((c) => c.kind === only) : found.items) : []
  if (nothing && found.status === 'ready' && items.length === 0) return null
  return (
    <section className={styles.section} aria-labelledby="find-ask">
      <div className={styles.discoverHeader}>
        <h2 id="find-ask" className={styles.sectionTitle}>
          {t('requests.search.title')}
        </h2>
        <span className={styles.discoverWhere}>{t(WHERE[scope])}</span>
      </div>
      {(found.status === 'loading' || (found.status === 'idle' && dq !== q)) && <CandidateSkeleton square={kind === 'music'} />}
      {found.status === 'error' && (
        <p className={styles.problem} role="status">
          <Icon name="cloud" size={18} /> {found.message}
        </p>
      )}
      {found.status === 'ready' && items.length === 0 && <p className={styles.muted}>{t('requests.search.nothingMore')}</p>}
      {found.status === 'ready' && items.length > 0 && (
        <ul className={styles.candidates}>
          {items.map((c) => (
            <KidCandidateRow key={`${c.kind}-${c.tmdbId || c.extId || c.title}`} c={c} />
          ))}
        </ul>
      )}
    </section>
  )
}

/** A result: its art, kind and year, title (and artist or author), and «Попросить» or the request's state. */
function KidCandidateRow({ c }: { c: KidCandidate }) {
  const [failed, setFailed] = useState(false)
  const video = isVideo(c.kind)
  // albums and books: placeholder art, whatever the answer carries
  const poster = video ? imageUrl(c.poster, 480) : ''
  const artist = c.kind === 'comic' ? '' : c.subtitle
  const ask: KidRequestAsk = video
    ? { kind: c.kind, tmdbId: c.tmdbId, title: c.title }
    : { kind: c.kind, extId: c.extId, title: c.title, subtitle: artist, year: c.year, aliases: c.aliases }
  const art =
    poster && !failed ? (
      <img src={poster} alt="" loading="lazy" onError={() => setFailed(true)} />
    ) : (
      <Icon name={video ? (c.kind === 'series' ? 'tv' : 'film') : (KIND_ICON[c.kind] ?? 'book')} size={28} />
    )
  // the core's title page (every build, kids included)
  const linked = video
  return (
    <li className={styles.candidate}>
      {linked ? (
        <Link to={titlePath(c)} className={styles.poster} aria-label={c.title}>
          {art}
        </Link>
      ) : (
        <span className={`${styles.poster} ${c.kind === 'music' ? styles.square : ''}`}>{art}</span>
      )}
      <div className={styles.info}>
        <p className={styles.kicker}>{[kindLabel(c.kind), c.year || ''].filter(Boolean).join(' · ')}</p>
        {linked ? (
          <Link to={titlePath(c)} className={styles.name}>
            {c.title}
          </Link>
        ) : (
          <p className={styles.name}>{c.title}</p>
        )}
        {artist && <p className={styles.byline}>{artist}</p>}
      </div>
      <div className={styles.footer}>
        <div className={styles.buttons}>{c.kidRequest && <RequestButton ask={ask} state={c.kidRequest} />}</div>
      </div>
    </li>
  )
}

function CandidateSkeleton({ square }: { square: boolean }) {
  const t = useT()
  return (
    <ul className={styles.candidates} aria-busy="true" aria-label={t('requests.search.searching')}>
      {[0, 1, 2].map((i) => (
        <li key={i} className={styles.candidate}>
          <div className={`${styles.poster} ${square ? styles.square : ''} ${styles.skeleton}`} />
          <div className={styles.info}>
            <div className={styles.skeletonLine} style={{ width: '30%' }} />
            <div className={styles.skeletonLine} style={{ width: '55%', height: 18 }} />
            <div className={styles.skeletonLine} style={{ width: '80%' }} />
          </div>
        </li>
      ))}
    </ul>
  )
}
