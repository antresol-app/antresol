// The words of a folder check: how full it is, what the server found.
import type { DirCheck, FolderRow } from '../../api/storage'
import type { useT } from '../../i18n'

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

