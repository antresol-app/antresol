// A movie's collection (franchise): the backdrop, the name and overview, and
// every part in release order (/item/:id/collection, from the movie's page).
// Parts the library has are its cards (watched marked); in the sources build an
// adult also sees the missing ones, which open their title page.
import { APP_NAME } from '../brand'
import { useEffect, useState } from 'react'
import { useParams } from 'react-router'
import { ApiError, getCollection, imageUrl } from '../api/client'
import type { CollectionRow } from '../api/types'
import { ButtonLink, Button } from '../ui/Button'
import { Icon } from '../components/Icon'
import { PosterGrid } from '../components/PosterGrid'
import { t as tr, useT } from '../i18n'
import detail from './ItemDetail.module.css'
import styles from './Collection.module.css'

type State = { status: 'loading' } | { status: 'error'; notFound: boolean; message: string } | { status: 'ready'; row: CollectionRow }

export function Collection() {
  const t = useT()
  const id = Number(useParams().id)
  const [state, setState] = useState<State>({ status: 'loading' })
  const [attempt, setAttempt] = useState(0)

  useEffect(() => {
    const ac = new AbortController()
    setState({ status: 'loading' })
    getCollection(id, ac.signal)
      .then((row) => setState({ status: 'ready', row }))
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        const notFound = e instanceof ApiError && (e.status === 404 || e.status === 400)
        setState({ status: 'error', notFound, message: e instanceof ApiError ? e.message : tr('common.loadFailed') })
      })
    return () => ac.abort()
  }, [id, attempt])

  const name = state.status === 'ready' ? t('item.collection', { name: state.row.name }) : ''
  useEffect(() => {
    if (name) document.title = `${name} — ${APP_NAME}`
  }, [name])

  if (state.status === 'loading') return <div className={styles.loading} aria-busy="true" aria-label={t('common.loading')} />
  if (state.status === 'error') {
    return (
      <div className={detail.error} role="alert">
        <Icon name={state.notFound ? 'info' : 'cloud'} size={44} strokeWidth={1.5} />
        <h1>{state.notFound ? t('item.collection.notFound') : t('common.loadFailed')}</h1>
        {!state.notFound && <p>{state.message}</p>}
        {state.notFound ? (
          <ButtonLink to={`/item/${id}`} variant="secondary">
            {t('common.back')}
          </ButtonLink>
        ) : (
          <Button variant="secondary" onClick={() => setAttempt((n) => n + 1)}>
            {t('common.retry')}
          </Button>
        )}
      </div>
    )
  }

  const { row } = state
  const backdrop = imageUrl(row.backdrop || row.poster, 1280)
  // the library's own cards (a missing part's card has id 0)
  const inLibrary = row.parts.filter((p) => p.id > 0).length
  const meta = [t('item.collection.parts', { count: row.parts.length })]
  if (inLibrary < row.parts.length) meta.push(t('item.collection.inLibrary', { count: inLibrary }))

  return (
    <article className={detail.page} aria-labelledby="collection-title">
      <div className={`${detail.hero} ${styles.hero}`}>
        <div className={detail.backdrop} aria-hidden>
          {backdrop && <img src={backdrop} alt="" decoding="async" draggable={false} />}
        </div>
        <div className={detail.scrim} aria-hidden />
        <div className={detail.copy}>
          <h1 id="collection-title" className={detail.title}>
            {name}
          </h1>
          <p className={detail.meta}>
            {meta.map((m, i) => (
              <span key={m}>
                {i > 0 && <span className={detail.sep}>·</span>}
                {m}
              </span>
            ))}
          </p>
          {row.overview && (
            <div className={detail.overviewWrap}>
              <p className={`${detail.overview} ${detail.expanded}`}>{row.overview}</p>
            </div>
          )}
        </div>
      </div>
      <div className={styles.parts}>
        <PosterGrid cards={row.parts} label={name} />
      </div>
    </article>
  )
}
