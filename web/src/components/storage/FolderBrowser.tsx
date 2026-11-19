// «Добавить папку»: the folder browser of the wizard and «Настройки сервера».
// It walks the folders the server was given — the media folder and the other
// folders mounted into it (a separate disk for one kind, an adults-only
// share) — and nothing else: the server lists one folder at a time and
// refuses anything outside them. The folder shown carries the server's real
// write test and how many entries it holds; «Выбрать» adds it to the kind.
import { useEffect, useState } from 'react'
import { browseFolders, type BrowseView, type FolderKind } from '../../api/storage'
import { errorText } from '../../api/client'
import { useT } from '../../i18n'
import { GroupedSection, ListLinkRow, ListRow } from '../../ui/List'
import { Notice } from '../../ui/Notice'
import { Select } from '../../ui/Select'
import { Sheet } from '../../ui/Sheet'
import { itemsText } from './format'
import styles from './Storage.module.css'

export function FolderBrowser({ kind, taken, onPick, onCancel }: {
  kind: FolderKind
  /** Folders (absolute) that already are library folders: not picked twice. */
  taken: string[]
  onPick: (path: string) => void
  onCancel: () => void
}) {
  const t = useT()
  const [path, setPath] = useState('')
  const [view, setView] = useState<BrowseView | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    const ac = new AbortController()
    setLoading(true)
    browseFolders(path, ac.signal).then(
      (v) => {
        setView(v)
        setError('')
        setLoading(false)
      },
      (e: unknown) => {
        if (ac.signal.aborted) return
        setError(errorText(e, t('storage.browse.failed')))
        setLoading(false)
      },
    )
    return () => ac.abort()
  }, [path, t])

  const kindName = t(`storage.kind.${kind}`)
  const here = view?.path ?? ''
  const place = view?.places.find((p) => here === p || here.startsWith(p.endsWith('/') ? p : p + '/')) ?? ''
  const isMediaRoot = Boolean(view && here === view.places[0])
  const already = Boolean(view?.library) || taken.includes(here)
  const status = !view
    ? ''
    : view.dir.status === 'ok'
      ? t('storage.browse.writable', { items: itemsText(view.dir, t) })
      : view.dir.status === 'readonly'
        ? t('storage.browse.readonly', { items: itemsText(view.dir, t) })
        : t('storage.status.unreadable')
  const why = isMediaRoot ? t('storage.browse.notRoot') : already ? t('storage.browse.taken') : ''

  return (
    <Sheet
      title={t('storage.browse.title', { kind: kindName })}
      role="dialog"
      size="large"
      onCancel={onCancel}
      busy={loading}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onCancel },
        { label: t('storage.browse.pick'), kind: 'primary', disabled: !view || loading || isMediaRoot || already || view.dir.status === 'unreadable', run: () => onPick(here) },
      ]}
    >
      {error && <Notice tone="destructive">{error}</Notice>}
      {view && view.places.length > 1 && (
        <GroupedSection>
          <ListRow label={t('storage.browse.place')}>
            <Select
              label={t('storage.browse.place')}
              placement="bottom-end"
              value={place}
              options={view.places.map((p, i) => ({ value: p, label: p, detail: i === 0 ? t('storage.browse.media') : t('storage.browse.mount') }))}
              onChange={(p) => setPath(p)}
            />
          </ListRow>
        </GroupedSection>
      )}
      {view && view.places.length === 0 && <Notice tone="warning">{t('storage.browse.noPlaces')}</Notice>}
      {view && view.path && (
        <GroupedSection title={<span className={styles.mono}>{view.path}</span>} footer={why || undefined}>
          <ListRow label={t('storage.browse.here')} status={<span className={view.dir.status === 'ok' ? styles.fine : styles.problem}>{status}</span>} />
          {view.parent && <ListLinkRow label={t('storage.browse.up')} icon="folder" tint="gray" onOpen={() => setPath(view.parent)} />}
          {view.dirs.map((d) => (
            <ListLinkRow
              key={d.path}
              label={d.name}
              icon="folder"
              tint="blue"
              value={itemsText(d, t)}
              onOpen={() => setPath(d.path)}
            />
          ))}
          {view.dirs.length === 0 && <ListRow label={t('storage.browse.noSubfolders')} />}
          {view.truncated && <ListRow label={t('storage.browse.truncated')} />}
        </GroupedSection>
      )}
    </Sheet>
  )
}
