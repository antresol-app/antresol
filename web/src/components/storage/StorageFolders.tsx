// What the server sees under the media folder, and which folder is which
// library: one row per kind with its status (found with a count, missing
// with «Создать», not writable) and a pop-up of the folders that are there.
import type { DirCheck, FolderRow, StorageView } from '../../api/storage'
import { FOLDER_KINDS } from '../../api/storage'
import { useT } from '../../i18n'
import { Button } from '../../ui/Button'
import { GroupedSection, ListRow } from '../../ui/List'
import { Notice } from '../../ui/Notice'
import { Select, type SelectOption } from '../../ui/Select'
import type { Storage } from './useStorage'
import styles from './Storage.module.css'

type T = ReturnType<typeof useT>

/** «12 элементов», «больше 9999 элементов», «пусто». */
export function itemsText(d: Pick<DirCheck, 'items' | 'more'>, t: T): string {
  if (d.more) return t('storage.itemsMore', { count: d.items })
  return d.items === 0 ? t('storage.empty') : t('storage.items', { count: d.items })
}

/** The status line of a library folder. */
export function folderStatus(row: FolderRow, t: T): string {
  switch (row.status) {
    case 'ok':
      // the server makes empty default folders itself: «пусто» beside one with files is not a finished pick
      if (row.suggest && row.items === 0 && !row.more) return t('storage.status.emptySuggest', { name: row.suggest })
      return t('storage.status.ok', { items: itemsText(row, t) })
    case 'missing':
      return row.suggest ? t('storage.status.missingSuggest', { name: row.suggest }) : t('storage.status.missing')
    case 'notdir':
      return t('storage.status.notdir')
    case 'unreadable':
      return t('storage.status.unreadable')
    default:
      return t('storage.status.readonly')
  }
}

/** The folders the pop-up offers: those under the media folder, and the current one when it is not among them. */
function optionsFor(view: StorageView, current: string, t: T): SelectOption<string>[] {
  const opts: SelectOption<string>[] = view.entries.map((e) => ({ value: e.name, label: e.name, detail: itemsText(e, t) }))
  if (current && !opts.some((o) => o.value === current)) opts.unshift({ value: current, label: current, detail: t('storage.status.missing') })
  return opts
}

/** `entries`: also list what is under the media folder (the wizard does; the settings' pop-ups already say it). */
export function StorageFolders({ storage, title, entries = true }: { storage: Storage; title?: string; entries?: boolean }) {
  const t = useT()
  const { view, paths, busy } = storage
  if (!view || !paths) return null
  const root = view.root.path
  const rootGone = view.root.status === 'missing' || view.root.status === 'notdir'
  return (
    <>
      {rootGone ? (
        <Notice tone="destructive">{t('storage.rootMissing', { root })}</Notice>
      ) : (
        view.offline && <Notice tone="destructive">{t('storage.diskOffline', { root })}</Notice>
      )}
      {entries && !rootGone && (
        <GroupedSection title={t('storage.rootTitle', { root })}>
          {view.entries.length === 0 ? (
            <ListRow label={t('storage.rootEmpty', { root })} />
          ) : (
            view.entries.map((e) => (
              <ListRow key={e.name} label={<span className={styles.mono}>{e.name}</span>}>
                <span className={styles.count}>{itemsText(e, t)}</span>
              </ListRow>
            ))
          )}
        </GroupedSection>
      )}
      <GroupedSection title={title ?? t('storage.libraries')}>
        {FOLDER_KINDS.map((kind) => {
          const row = view.folders.find((f) => f.kind === kind)
          if (!row) return null
          const name = t(`storage.kind.${kind}`)
          const emptyDefault = row.status === 'ok' && row.items === 0 && !row.more && Boolean(row.suggest)
          const tone = row.status === 'ok' && !emptyDefault ? styles.fine : row.status === 'missing' || emptyDefault ? styles.neutral : styles.problem
          return (
            <ListRow
              key={kind}
              stack
              label={name}
              status={<span className={tone}>{folderStatus(row, t)}</span>}
            >
              <div className={styles.rowActions}>
                {row.suggest && (
                  <Button size="small" variant="secondary" disabled={busy} onClick={() => void storage.pick(kind, row.suggest!)}>
                    {t('storage.useSuggest', { name: row.suggest })}
                  </Button>
                )}
                {row.status === 'missing' && view.canCreate && (
                  <Button size="small" variant="secondary" disabled={busy} onClick={() => void storage.create(row.path)}>
                    {t('storage.create')}
                  </Button>
                )}
                <Select
                  label={t('storage.pick', { kind: name })}
                  placement="bottom-end"
                  value={paths[kind]}
                  options={optionsFor(view, paths[kind], t)}
                  disabled={busy}
                  onChange={(p) => void storage.pick(kind, p)}
                />
              </div>
            </ListRow>
          )
        })}
      </GroupedSection>
    </>
  )
}
