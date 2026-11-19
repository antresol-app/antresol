// The library's folders as the server sees them: a group per kind with each
// of its folders (the first is where the kind's downloads and uploads go),
// its status (found with a count, missing with «Создать», not writable), its
// marks («Только для взрослых», «Скрыть из общих подборок») and «⋯» to make
// it the first, mark it or take it out; «Добавить папку…» opens the folder
// browser over what is mounted into the server.
import { useState } from 'react'
import type { FolderKind, FolderRow } from '../../api/storage'
import { FOLDER_KINDS } from '../../api/storage'
import { Icon } from '../Icon'
import { useT } from '../../i18n'
import { Badge } from '../../ui/Badge'
import { Button } from '../../ui/Button'
import { IconButton } from '../../ui/IconButton'
import { GroupedSection, ListRow } from '../../ui/List'
import { Menu, MenuItem, MenuSeparator } from '../../ui/Menu'
import { Notice } from '../../ui/Notice'
import { Sheet } from '../../ui/Sheet'
import { FolderBrowser } from './FolderBrowser'
import { folderStatus, itemsText } from './format'
import type { Removal, Storage } from './useStorage'
import styles from './Storage.module.css'

/** One folder of a kind. */
function FolderLine({ storage, row, only }: { storage: Storage; row: FolderRow; only: boolean }) {
  const t = useT()
  const { view, busy } = storage
  const name = t(`storage.kind.${row.kind}`)
  const emptyDefault = row.status === 'ok' && row.items === 0 && !row.more && Boolean(row.suggest)
  const tone = row.status === 'ok' && !emptyDefault ? styles.fine : row.status === 'missing' || emptyDefault ? styles.neutral : styles.problem
  const label = (
    <span className={styles.folderLabel}>
      <span className={styles.mono}>{row.path}</span>
      {(row.primary && !only) || row.adult || row.unlisted ? (
        <span className={styles.badges}>
          {row.primary && !only && <Badge tone="outline">{t('storage.primary')}</Badge>}
          {row.adult && <Badge tone="warning">{t('storage.adult')}</Badge>}
          {row.unlisted && <Badge>{t('storage.unlisted')}</Badge>}
        </span>
      ) : null}
    </span>
  )
  const more = t('storage.folderMenu', { path: row.path })
  return (
    <ListRow label={label} status={<span className={tone}>{folderStatus(row, t)}</span>}>
      <div className={styles.rowActions}>
        {row.suggest && (
          <Button size="small" variant="secondary" disabled={busy} onClick={() => void storage.replace(row.kind, row.path, row.suggest!)}>
            {t('storage.useSuggest', { name: row.suggest })}
          </Button>
        )}
        {row.status === 'missing' && view?.canCreate && row.abs.startsWith(view.root.path + '/') && (
          <Button size="small" variant="secondary" disabled={busy} onClick={() => void storage.create(row.path)}>
            {t('storage.create')}
          </Button>
        )}
        <Menu label={more} placement="bottom-end" trigger={(p) => <IconButton {...p} size="sm" icon="ellipsis" label={more} disabled={busy} />}>
          {(close) => (
            <>
              {!row.primary && (
                <MenuItem
                  label={t('storage.makePrimary')}
                  detail={t('storage.makePrimary.hint', { kind: name })}
                  onSelect={() => {
                    close()
                    void storage.makePrimary(row.kind, row.path)
                  }}
                />
              )}
              <MenuItem
                multi
                checked={row.adult}
                label={t('storage.adult')}
                detail={t('storage.adult.hint')}
                onSelect={() => {
                  close()
                  void storage.setFlag(row.kind, row.path, 'adult', !row.adult)
                }}
              />
              <MenuItem
                multi
                checked={row.unlisted}
                label={t('storage.unlistedFull')}
                detail={t('storage.unlisted.hint')}
                onSelect={() => {
                  close()
                  void storage.setFlag(row.kind, row.path, 'unlisted', !row.unlisted)
                }}
              />
              <MenuSeparator />
              <MenuItem
                destructive
                icon={<Icon name="minusCircle" size={18} />}
                label={t('storage.removeFolder')}
                detail={only ? t('storage.removeFolder.only') : row.indexed > 0 ? t('storage.removeFolder.hint', { count: row.indexed }) : undefined}
                disabled={only}
                onSelect={() => {
                  close()
                  void storage.remove(row.kind, row.path)
                }}
              />
            </>
          )}
        </Menu>
      </div>
    </ListRow>
  )
}

/** `entries`: also list what is under the media folder (the wizard does; the settings have the folder browser). */
export function StorageFolders({ storage, entries = true }: { storage: Storage; entries?: boolean }) {
  const t = useT()
  const [browsing, setBrowsing] = useState<FolderKind | null>(null)
  const { view, busy } = storage
  if (!view) return null
  const root = view.root.path
  const rootGone = view.root.status === 'missing' || view.root.status === 'notdir'
  const taken = view.folders.map((f) => f.abs)
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
      {FOLDER_KINDS.map((kind) => {
        const rows = view.folders.filter((f) => f.kind === kind)
        const name = t(`storage.kind.${kind}`)
        return (
          <GroupedSection key={kind} title={name} footer={rows.length > 1 ? t('storage.kindFooter', { path: rows[0]!.path }) : undefined}>
            {rows.map((row) => (
              <FolderLine key={row.path} storage={storage} row={row} only={rows.length === 1} />
            ))}
            <ListRow label={t('storage.addFolder.label', { kind: name })}>
              <Button size="small" variant="plain" icon="plus" disabled={busy} onClick={() => setBrowsing(kind)}>
                {t('storage.addFolder')}
              </Button>
            </ListRow>
          </GroupedSection>
        )
      })}
      {browsing && (
        <FolderBrowser
          kind={browsing}
          taken={taken}
          onCancel={() => setBrowsing(null)}
          onPick={(p) => {
            const kind = browsing
            setBrowsing(null)
            void storage.add(kind, p)
          }}
        />
      )}
    </>
  )
}

/** Asks before a save takes folders out whose titles leave the library (their files stay on the disk). */
export function RemoveConfirm({ removals, busy, onConfirm, onCancel }: { removals: Removal[]; busy: boolean; onConfirm: () => void; onCancel: () => void }) {
  const t = useT()
  const total = removals.reduce((n, r) => n + r.indexed, 0)
  return (
    <Sheet
      title={t('storage.removeConfirm.title')}
      message={
        <>
          <p>{total > 0 ? t('storage.removeConfirm.message', { count: total }) : t('storage.removeConfirm.messageAny')}</p>
          {removals.length > 0 && (
            <ul className={styles.removeList}>
              {removals.map((r) => (
                <li key={r.path}>
                  <span className={styles.mono}>{r.path}</span> · {t('storage.removeConfirm.titles', { count: r.indexed })}
                </li>
              ))}
            </ul>
          )}
        </>
      }
      busy={busy}
      onCancel={onCancel}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onCancel },
        { label: t('storage.removeConfirm.ok'), kind: 'destructive', run: onConfirm },
      ]}
    />
  )
}
