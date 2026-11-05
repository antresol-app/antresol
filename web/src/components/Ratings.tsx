// IMDb and Rotten Tomatoes ratings of a title page: fetched after the page
// (the server may ask Wikidata), shown as chips on the metadata line and as
// rows of «Информация». Nothing known: nothing shown.
import { useEffect, useState } from 'react'
import { getRatings } from '../api/client'
import type { Ratings } from '../api/types'
import { formatPercent, locale, useT, type TFunction } from '../i18n'
import styles from './Ratings.module.css'

/** The ratings of an IMDb id; undefined while loading, without an id or on a failure. */
export function useRatings(imdbId?: string): Ratings | undefined {
  const [state, setState] = useState<{ id: string; r: Ratings } | undefined>()
  useEffect(() => {
    if (!imdbId) return
    const ac = new AbortController()
    getRatings(imdbId, ac.signal)
      .then((r) => setState({ id: imdbId, r }))
      .catch(() => {}) // optional: a failure shows nothing
    return () => ac.abort()
  }, [imdbId])
  return imdbId && state?.id === imdbId ? state.r : undefined
}

const imdbScore = (n: number) => n.toLocaleString(locale(), { minimumFractionDigits: 1, maximumFractionDigits: 1 })
const percent = formatPercent

function votes(t: TFunction, n: number) {
  return t('item.ratings.votes', { count: n, n: n.toLocaleString(locale()) })
}

/** The chips of the metadata line: «IMDb 7,4» «RT 92%». `className`: the page's badge. */
export function RatingChips({ ratings, className }: { ratings?: Ratings; className?: string }) {
  const t = useT()
  const imdb = ratings?.imdb
  const rt = ratings?.rottenTomatoes?.tomatometer
  return (
    <>
      {imdb && (
        <span
          className={[className, styles.chip].join(' ')}
          title={`${t('item.ratings.imdb', { rating: imdbScore(imdb.rating) })} · ${votes(t, imdb.votes)}\n${t('item.ratings.imdbCredit')}`}
          aria-label={t('item.ratings.imdb', { rating: imdbScore(imdb.rating) })}
          data-rating="imdb"
        >
          <span className={styles.source} aria-hidden>
            IMDb
          </span>
          <span aria-hidden>{imdbScore(imdb.rating)}</span>
        </span>
      )}
      {rt ? (
        <span
          className={[className, styles.chip].join(' ')}
          title={t('item.ratings.rt', { pct: percent(rt) })}
          aria-label={t('item.ratings.rt', { pct: percent(rt) })}
          data-rating="rt"
        >
          <span className={styles.source} aria-hidden>
            RT
          </span>
          <span aria-hidden>{percent(rt)}</span>
        </span>
      ) : null}
    </>
  )
}

/** Rows of «Информация»: [label, value] (empty values are left out by the page). */
export function ratingRows(t: TFunction, r?: Ratings): [string, string][] {
  const imdb = r?.imdb
  const rt = r?.rottenTomatoes
  const rtParts = [
    rt?.tomatometer ? t('item.ratings.critics', { pct: percent(rt.tomatometer) }) : '',
    rt?.audience ? t('item.ratings.audience', { pct: percent(rt.audience) }) : '',
  ].filter(Boolean)
  return [
    [t('item.info.imdb'), imdb ? `${imdbScore(imdb.rating)} · ${votes(t, imdb.votes)}` : ''],
    [t('item.info.rottenTomatoes'), rtParts.length === 1 && rt?.tomatometer ? percent(rt.tomatometer) : rtParts.join(' · ')],
  ]
}
