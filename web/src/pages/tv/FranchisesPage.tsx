// «Франшизы» (/tv/manage/franchises, admin): the franchises the franchise
// channels play — TMDB collections, or the AI helper's (titles of the same
// universe added, story order) — each with its titles and its channel;
// «Изменить порядок» opens the titles as a SortableList (drag, ↑/↓,
// «Убрать» hides one) whose order survives later recomputes; «Сбросить
// порядок» goes back to the computed one; «Пересчитать» asks for a pass now.
import { useEffect, useState } from 'react'
import { APP_NAME } from '../../brand'
import { errorText } from '../../api/client'
import { getFranchises, refreshFranchises, resetFranchiseOrder, setFranchiseOrder, type Franchise, type FranchiseItem } from '../../api/channels'
import { Icon } from '../../components/Icon'
import { useT } from '../../i18n'
import { ago } from '../../state/format'
import { useLoad } from '../../state/useLoad'
import { Badge } from '../../ui/Badge'
import { Button } from '../../ui/Button'
import { GroupedSection, ListRow } from '../../ui/List'
import { Notice } from '../../ui/Notice'
import { Sheet } from '../../ui/Sheet'
import { SortableList, SortableRowText } from '../../ui/SortableList'
import { Toast } from '../../ui/Toast'
import styles from './Tv.module.css'

export function FranchisesPage() {
  const t = useT()
  const [editing, setEditing] = useState<Franchise | null>(null)
  const [toast, setToast] = useState('')
  const { state, load } = useLoad((signal) => getFranchises(signal), [], {
    message: (e) => errorText(e, t('common.loadFailed')),
    events: [],
  })
  useEffect(() => {
    document.title = `${t('tv.franchises.title')} — ${APP_NAME}`
  }, [t])
  useEffect(() => {
    if (!toast) return
    const id = window.setTimeout(() => setToast(''), 4000)
    return () => window.clearTimeout(id)
  }, [toast])
  const data = state.status === 'ready' ? state.data : null
  // a pass running: look again in a while
  useEffect(() => {
    if (!data?.status.running) return
    const id = window.setTimeout(() => load(true), 5000)
    return () => window.clearTimeout(id)
  }, [data, load])

  const refresh = () =>
    refreshFranchises().then(
      () => {
        setToast(t('tv.franchises.refreshing'))
        window.setTimeout(() => load(true), 1500)
      },
      (e: unknown) => setToast(errorText(e, t('common.genericError'))),
    )

  const status = data?.status
  const statusLine = !status
    ? ''
    : status.running
      ? t('tv.franchises.running')
      : [status.model ? t('tv.franchises.byAI') : t('tv.franchises.byTMDB'), status.at ? t('tv.franchises.at', { ago: ago(status.at) }) : ''].filter(Boolean).join(' · ')

  return (
    <div className={styles.page}>
      <div className={styles.column}>
        <header className={styles.columnHeader}>
          <h1 className={styles.title}>{t('tv.franchises.title')}</h1>
          {data?.available && (
            <div className={styles.actions}>
              <Button variant="secondary" icon="restart" disabled={status?.running} onClick={refresh}>
                {t('tv.franchises.refresh')}
              </Button>
            </div>
          )}
        </header>
        {state.status === 'error' && (
          <div className={styles.empty} role="alert">
            <p>{state.message}</p>
            <Button variant="secondary" onClick={() => load()}>
              {t('common.retry')}
            </Button>
          </div>
        )}
        {data && !data.available && (
          <div className={styles.empty}>
            <Icon name="broadcast" size={48} strokeWidth={1.4} />
            <h2>{t('tv.franchises.unavailable')}</h2>
            <p>{t('tv.franchises.unavailableHint')}</p>
          </div>
        )}
        {data?.available && (
          <>
            {statusLine && <p className={styles.franchiseStatus}>{statusLine}</p>}
            {status?.error && (
              <Notice tone="warning" title={t('tv.franchises.aiFailed')}>
                {status.error}
              </Notice>
            )}
            {status?.incomplete && <Notice tone="accent">{t('tv.franchises.incomplete')}</Notice>}
            {data.franchises.length === 0 ? (
              <div className={styles.empty}>
                <h2>{t('tv.franchises.empty')}</h2>
                <p>{t('tv.franchises.emptyHint')}</p>
              </div>
            ) : (
              <GroupedSection title={t('tv.franchises.list')} footer={t('tv.franchises.footer')}>
                {data.franchises.map((f) => {
                  const shown = f.items.filter((x) => !x.hidden).length
                  return (
                    <ListRow
                      key={f.key}
                      label={
                        <span className={styles.rowName}>
                          <span>{f.name}</span>
                          <Badge>
                            {f.source === 'ai' ? t('tv.franchises.sourceAI') : f.source === 'wikidata' ? t('tv.franchises.sourceWikidata') : t('tv.franchises.sourceTMDB')}
                          </Badge>
                          {f.manual && <Badge tone="accent">{t('tv.franchises.manual')}</Badge>}
                        </span>
                      }
                      hint={[t('tv.franchises.titles', { count: shown }), f.channelId ? t('tv.franchises.hasChannel') : t('tv.franchises.noChannel')].join(' · ')}
                    >
                      <Button variant="secondary" size="small" onClick={() => setEditing(f)}>
                        {t('tv.franchises.edit')}
                      </Button>
                    </ListRow>
                  )
                })}
              </GroupedSection>
            )}
          </>
        )}
      </div>
      {editing && (
        <FranchiseOrderSheet
          franchise={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            load(true)
          }}
        />
      )}
      {toast && <Toast>{toast}</Toast>}
    </div>
  )
}

const itemKey = (x: FranchiseItem) => String(x.id)

/** The franchise's titles as an ordered list; a removed one is hidden (and can come back). */
export function FranchiseOrderSheet({ franchise, onClose, onSaved }: { franchise: Franchise; onClose: () => void; onSaved: () => void }) {
  const t = useT()
  const [items, setItems] = useState(() => franchise.items.filter((x) => !x.hidden))
  const [hidden, setHidden] = useState(() => franchise.items.filter((x) => x.hidden))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const kind = (x: FranchiseItem) => (x.kind === 'series' ? t('tv.picks.kind.series') : t('tv.picks.kind.movie'))

  const run = (p: Promise<unknown>) => {
    setBusy(true)
    setError('')
    p.then(onSaved, (e: unknown) => {
      setError(errorText(e, t('common.genericError')))
      setBusy(false)
    })
  }
  const change = (next: FranchiseItem[]) => {
    const gone = items.filter((x) => !next.some((y) => y.id === x.id))
    setItems(next)
    if (gone.length) setHidden((h) => [...h, ...gone])
  }
  const actions = [
    { label: t('common.cancel'), kind: 'cancel' as const, run: onClose },
    ...(franchise.manual ? [{ label: t('tv.franchises.reset'), run: () => run(resetFranchiseOrder(franchise.key)) }] : []),
    {
      label: t('common.save'),
      kind: 'primary' as const,
      disabled: items.length === 0,
      run: () => run(setFranchiseOrder(franchise.key, items.map((x) => x.id), hidden.map((x) => x.id))),
    },
  ]
  return (
    <Sheet title={t('tv.franchises.orderTitle', { name: franchise.name })} role="dialog" size="large" busy={busy} onCancel={onClose} actions={actions}>
      {franchise.error && <Notice tone="warning">{t('tv.franchises.itemError', { error: franchise.error })}</Notice>}
      <GroupedSection title={t('tv.franchises.order')} footer={error || t('tv.franchises.orderHint')}>
        <ListRow label={t('tv.franchises.order')} wide>
          <SortableList
            label={t('tv.franchises.order')}
            items={items}
            getKey={itemKey}
            itemLabel={(x) => x.title}
            renderItem={(x) => (
              <SortableRowText leading={<Icon name={x.kind === 'series' ? 'tv' : 'film'} size={16} />} title={x.title} detail={[kind(x), x.year].filter(Boolean).join(' · ')} />
            )}
            onChange={change}
            removable
            min={1}
            moveButtons
          />
        </ListRow>
      </GroupedSection>
      {hidden.length > 0 && (
        <GroupedSection title={t('tv.franchises.hidden')}>
          {hidden.map((x) => (
            <ListRow key={x.id} label={x.title} hint={[kind(x), x.year].filter(Boolean).join(' · ')}>
              <Button
                variant="secondary"
                size="small"
                onClick={() => {
                  setHidden((h) => h.filter((y) => y.id !== x.id))
                  setItems((l) => [...l, x])
                }}
              >
                {t('tv.franchises.restore')}
              </Button>
            </ListRow>
          ))}
        </GroupedSection>
      )}
    </Sheet>
  )
}
