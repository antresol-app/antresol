// «Файлы» in «Управление»: what takes the space. The disk as one bar
// (Фильмы / Сериалы / Торренты / Кеш / Свободно), then every title of the
// library with its size and when anyone last played it, each with «Удалить»
// (to the Корзина, with a confirmation). A switcher picks the section and the
// menu sorts (size, last watched, name, date added) and filters (watched,
// never, not for half a year / a year), as in «Загрузки»; both live in the URL
// (?kind, ?sort, ?watch). Browsing files is the Finder-style «Файлы» page.
import { APP_NAME } from '../../brand'
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router'
import { ApiError, getStorage, imageUrl } from '../../api/client'
import { getUsage, trashFiles } from '../../api/files'
import type { FileUsage, StorageItem, StorageReport } from '../../api/types'
import { ButtonLink, Button } from '../../ui/Button'
import { DiskMeter, segmentsOf } from '../../components/DiskMeter'
import { Icon } from '../../components/Icon'
import { SegmentedControl } from '../../ui/SegmentedControl'
import { Sheet } from '../../ui/Sheet'
import { mediaKindIcon, mediaSegmentIcon, useMediaSegments, useUrlView, ViewMenu, ViewToolbar, type ViewSpec } from '../../components/ViewMenu'
import { itemHref } from '../../components/Card'
import { resetUsageCache } from '../files/StorageBar'
import { ago, formatSize } from '../../state/format'
import { locale, useT, type Key } from '../../i18n'
import { FILE_SEGMENTS, FILE_SORTS, FILE_WATCHES, filterStorage, sortStorage, type FileSegment, type FileSort, type FileView, type FileWatch } from './filesView'
import m from './Manage.module.css'
import styles from './Files.module.css'

type Confirm = { item: StorageItem; warnings?: string[] } | null

/** How many rows show before «Показать ещё» (a library is thousands of albums at most). */
const PAGE = 60

type Verb = 'watch' | 'listen' | 'read'
const kindVerb = (kind: StorageItem['kind']): Verb => (kind === 'album' || kind === 'audiobook' ? 'listen' : kind === 'book' || kind === 'comic' ? 'read' : 'watch')
const segmentVerb = (s: FileSegment): Verb => (s === 'music' ? 'listen' : s === 'books' ? 'read' : 'watch')
const AGO: Record<Verb, [Key, Key]> = {
  watch: ['mfiles.watchedAgo', 'mfiles.neverWatched'],
  listen: ['mfiles.listenedAgo', 'mfiles.neverListened'],
  read: ['mfiles.readAgo', 'mfiles.neverRead'],
}

function ItemRow({ it, onDelete }: { it: StorageItem; onDelete: () => void }) {
  const t = useT()
  const poster = imageUrl(it.poster, 160)
  const [agoKey, neverKey] = AGO[kindVerb(it.kind)]
  // never played: when it came, so a forgotten title stands out from a new one
  const watched = it.lastWatched ? t(agoKey, { ago: ago(it.lastWatched) }) : t('mfiles.addedAgo', { ago: ago(it.addedAt), watched: t(neverKey) })
  const what =
    it.kind === 'series'
      ? t('mfiles.seriesFiles', { count: it.files })
      : it.kind === 'album' && it.artist
        ? t('mfiles.albumBy', { artist: it.artist })
        : t(`common.kind.${it.kind}` as Key)
  const href = itemHref(it.kind, it.id)
  return (
    <li className={styles.item} data-testid="storage-item">
      <Link to={href} className={styles.poster} aria-label={it.title}>
        {poster ? <img src={poster} alt="" loading="lazy" /> : <Icon name={mediaKindIcon(it.kind)} size={20} />}
      </Link>
      <div className={styles.text}>
        <Link to={href} className={styles.name}>
          {it.title}
          {it.year ? <span className={styles.year}> ({it.year})</span> : null}
        </Link>
        <p className={styles.meta}>
          {what} · {watched}
        </p>
      </div>
      <span className={styles.size}>{formatSize(it.size)}</span>
      <Button size="small" variant="plain" icon="trash" className={styles.delete} disabled={it.trash.length === 0} onClick={onDelete} aria-label={t('mfiles.deleteTitle', { title: it.title })}>
        <span className={styles.deleteLabel}>{t('common.delete')}</span>
      </Button>
    </li>
  )
}


const FILE_VIEW: ViewSpec<FileView> = {
  segment: { param: 'kind', values: FILE_SEGMENTS },
  sort: { param: 'sort', values: FILE_SORTS },
  watch: { param: 'watch', values: FILE_WATCHES },
}

const SORT_LABEL: Record<FileSort, Key> = {
  size: 'mfiles.sort.size',
  watched: 'mfiles.sort.watched',
  name: 'mfiles.sort.name',
  added: 'mfiles.sort.added',
}
/** The list's title: what the order shows. */
/** «Давно не смотрели / слушали / открывали», as the rows and filters of the section say it. */
const STALE_TITLE: Record<Verb, Key> = { watch: 'mfiles.stale', listen: 'mfiles.staleListen', read: 'mfiles.staleRead' }
const SORT_TITLE: Record<FileSort, Key> = {
  size: 'mfiles.largest',
  watched: 'mfiles.stale',
  name: 'mfiles.byName',
  added: 'mfiles.recent',
}

function FilesMenu({ view, set }: { view: FileView; set: (p: Partial<FileView>) => void }) {
  const t = useT()
  const verb = segmentVerb(view.segment)
  return (
    <ViewMenu
      label={t('mfiles.view')}
      filtered={view.watch !== 'all'}
      resetLabel={t('mfiles.filter.reset')}
      onReset={() => set({ watch: 'all' })}
      groups={[
        {
          heading: t('common.sort'),
          options: FILE_SORTS.map((s) => ({ value: s, label: t(SORT_LABEL[s]), detail: s === 'watched' ? t('mfiles.sort.watchedDetail') : undefined })),
          value: view.sort,
          onChange: (sort: FileSort) => set({ sort }),
        },
        {
          heading: t(`mfiles.filter.${verb}.heading` as Key),
          options: FILE_WATCHES.map((w) => ({ value: w, label: t(w === 'all' ? 'mfiles.filter.all' : (`mfiles.filter.${verb}.${w}` as Key)) })),
          value: view.watch,
          onChange: (watch: FileWatch) => set({ watch }),
        },
      ]}
    />
  )
}

export function Files() {
  const t = useT()
  const [usage, setUsage] = useState<FileUsage | null>(null)
  const [usageFailed, setUsageFailed] = useState(false)
  const [report, setReport] = useState<StorageReport | null>(null)
  const [error, setError] = useState('')
  const [confirm, setConfirm] = useState<Confirm>(null)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState('')
  const [view, setView] = useUrlView(FILE_VIEW)
  const [shown, setShown] = useState(PAGE)

  const load = useCallback(() => {
    setUsageFailed(false)
    getUsage().then(setUsage, () => setUsageFailed(true))
    getStorage().then(
      (r) => {
        setReport(r)
        setError('')
      },
      (e: unknown) => setError(e instanceof ApiError ? e.message : t('common.loadFailed')),
    )
  }, [])
  useEffect(() => {
    document.title = `${t('common.files')} — ${APP_NAME}`
    load()
  }, [load, t])

  const remove = async (item: StorageItem, force: boolean) => {
    setBusy(true)
    try {
      const warnings: string[] = []
      for (const tr of item.trash) {
        const r = await trashFiles(tr.root, tr.paths, force)
        if (r.needsConfirm) warnings.push(...r.warnings)
        else if (r.error) throw new ApiError(400, r.error)
      }
      if (warnings.length > 0) {
        setConfirm({ item, warnings })
        return
      }
      setConfirm(null)
      setDone(t('mfiles.inTrash', { title: item.title }))
      resetUsageCache()
      setReport((r) => r && { items: r.items.filter((x) => x.id !== item.id) })
      window.setTimeout(load, 1500) // the scanner drops the item from the library
    } catch (e) {
      setConfirm(null)
      setError(e instanceof ApiError ? e.message : t('mfiles.deleteFailed'))
    } finally {
      setBusy(false)
    }
  }

  const all = report?.items ?? []
  const segments = useMediaSegments(all.map((x) => x.kind), view.segment, 'mfiles.seg.all')
  const list = useMemo(() => sortStorage(filterStorage(all, view), view.sort, locale()), [all, view.segment, view.sort, view.watch])
  const total = list.reduce((n, x) => n + x.size, 0)
  // a new view starts from the top of the list
  useEffect(() => setShown(PAGE), [view.segment, view.sort, view.watch])
  const setViewDone = (p: Partial<FileView>) => (setDone(''), setView(p))

  return (
    <section aria-labelledby="files-title">
      <header className={m.header}>
        <div>
          <h2 id="files-title" className={m.title}>
            {t('common.files')}
          </h2>
          <p className={m.subtitle}>{t('mfiles.subtitle')}</p>
        </div>
        <div className={m.headerActions}>
          <ButtonLink to="/files" variant="secondary" icon="folder">
            {t('mfiles.openAll')}
          </ButtonLink>
        </div>
      </header>

      <div className={styles.meter}>
        {usage ? (
          // no disk figures (a library-only server): no meter
          usage.total > 0 && <DiskMeter title={t('mfiles.mediaDisk')} total={usage.total} free={usage.free} segments={segmentsOf(usage.categories)} />
        ) : usageFailed ? (
          <div className={styles.usageError} role="alert">
            <Icon name="warning" size={18} /> {t('mfiles.usageFailed')}
            <Button size="small" variant="secondary" onClick={load}>
              {t('common.retry')}
            </Button>
          </div>
        ) : (
          <div className={m.spinnerRow} role="status">
            <span className={m.spinner} aria-hidden /> {t('mfiles.counting')}
          </div>
        )}
      </div>

      {error && (
        <div className={m.problem} role="alert">
          {error}
          {!report && (
            <Button size="small" variant="secondary" onClick={load}>
              {t('common.retry')}
            </Button>
          )}
        </div>
      )}
      {done && (
        <p className={`${m.groupFoot} ${m.ok}`} role="status" style={{ marginBottom: 12 }}>
          {done}
        </p>
      )}

      {report && all.length === 0 && <p className={m.groupFoot}>{t('mfiles.libraryEmpty')}</p>}
      {report && all.length > 0 && (
        <>
          <ViewToolbar>
            <SegmentedControl label={t('mfiles.seg.label')} segments={segments} value={view.segment} onChange={(segment) => setViewDone({ segment })} />
            <FilesMenu view={view} set={setViewDone} />
          </ViewToolbar>
          <h3 className={m.groupTitle}>{t(view.sort === 'watched' ? STALE_TITLE[segmentVerb(view.segment)] : SORT_TITLE[view.sort])}</h3>
          {list.length === 0 ? (
            <div className={m.empty}>
              <Icon name={mediaSegmentIcon(view.segment, 'folder')} size={40} strokeWidth={1.4} />
              <p>{t('mfiles.emptyFiltered')}</p>
              {view.watch !== 'all' && (
                <Button size="small" variant="secondary" onClick={() => setViewDone({ watch: 'all' })}>
                  {t('mfiles.filter.reset')}
                </Button>
              )}
            </div>
          ) : (
            <>
              <p className={m.groupFoot} style={{ marginTop: 0, marginBottom: 10 }} data-testid="storage-summary">
                {t('mfiles.summary', { count: list.length, size: formatSize(total) })}
              </p>
              <ul className={m.group}>
                {list.slice(0, shown).map((it) => (
                  <ItemRow key={it.id} it={it} onDelete={() => (setDone(''), setConfirm({ item: it }))} />
                ))}
              </ul>
              {list.length > shown && (
                <div className={styles.more}>
                  <Button size="small" variant="secondary" onClick={() => setShown((n) => n + PAGE)}>
                    {t('mfiles.showMore')}
                  </Button>
                </div>
              )}
            </>
          )}
        </>
      )}

      {confirm && (
        <Sheet
          title={!__LIBRARY_EDITION__ && confirm.warnings ? t('mfiles.seedingTitle') : t('mfiles.deleteConfirm', { title: confirm.item.title })}
          message={
            // another build's server warns before deleting some files
            !__LIBRARY_EDITION__ && confirm.warnings ? (
              <>
                {t('mfiles.seedingText')}
                <ul>
                  {confirm.warnings.slice(0, 4).map((w) => (
                    <li key={w}>{w}</li>
                  ))}
                </ul>
              </>
            ) : (
              t('mfiles.deleteText', { size: formatSize(confirm.item.size) })
            )
          }
          icon={<Icon name={confirm.warnings ? 'warning' : 'trash'} size={34} strokeWidth={1.6} />}
          busy={busy}
          onCancel={() => setConfirm(null)}
          actions={[
            { label: t('common.cancel'), kind: 'cancel', run: () => setConfirm(null) },
            { label: confirm.warnings ? t('mfiles.deleteAnyway') : t('common.delete'), kind: 'destructive', run: () => void remove(confirm.item, Boolean(confirm.warnings)) },
          ]}
        />
      )}
    </section>
  )
}
