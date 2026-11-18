// The write test: the media folder, each library folder and the transcoding
// folder, each with what the server found when it made and removed a file in
// it. When it may not, the fix as the server worded it: who it runs as, who
// owns the folder, a command and a compose line to copy.
import type { DirCheck, FolderWhy, StorageView } from '../../api/storage'
import { FOLDER_KINDS } from '../../api/storage'
import { useT } from '../../i18n'
import { GroupedSection, ListRow } from '../../ui/List'
import { Notice } from '../../ui/Notice'
import { CopyBlock } from './CopyBlock'
import styles from './Storage.module.css'

type T = ReturnType<typeof useT>

/** What the write test said about one path. */
export function permText(d: DirCheck, t: T): string {
  switch (d.status) {
    case 'ok':
      return t('storage.perm.ok')
    case 'missing':
      return t('storage.perm.missing')
    case 'notdir':
      return t('storage.status.notdir')
    case 'unreadable':
      return t('storage.perm.unreadable')
  }
  if (d.why === 'readonlyFS') return t('storage.perm.readonlyFS')
  if (d.why === 'other') return t('storage.perm.other', { error: d.error ?? '' })
  return t('storage.perm.denied')
}

/** Every path the write test is reported for, with its label. */
function checked(view: StorageView, t: T): { key: string; label: string; path: string; d: DirCheck }[] {
  const rows = [{ key: 'root', label: t('storage.perm.root'), path: view.root.path, d: view.root }]
  for (const k of FOLDER_KINDS) {
    const f = view.folders.find((x) => x.kind === k)
    if (f) rows.push({ key: k, label: t(`storage.kind.${k}`), path: f.abs, d: f })
  }
  if (view.transcode.path) rows.push({ key: 'transcode', label: t('storage.perm.transcode'), path: view.transcode.path, d: view.transcode })
  return rows
}

export const hasProblem = (d: DirCheck) => d.status === 'readonly' || d.status === 'unreadable' || d.status === 'notdir'

function Fix({ view }: { view: StorageView }) {
  const t = useT()
  const fix = view.fix
  if (!fix) return null
  const whys = new Set<FolderWhy | undefined>(
    [view.root, ...view.folders, view.transcode].filter((d) => d.status === 'readonly' || d.status === 'unreadable').map((d) => d.why),
  )
  const apply = 'docker compose up -d'
  return (
    <Notice className={styles.fix} title={t('storage.fix.title')}>
      {fix.paths.length > 0 && (
        <p className={styles.fixText}>
          {fix.owner ? t('storage.fix.who', { uid: fix.uid, gid: fix.gid, owner: fix.owner }) : t('storage.fix.whoNoOwner', { uid: fix.uid, gid: fix.gid })}
        </p>
      )}
      {fix.chown && (
        <>
          <p className={styles.fixText}>{fix.docker ? t('storage.fix.chown') : t('storage.fix.chownHost')}</p>
          <CopyBlock text={fix.chown} />
        </>
      )}
      {fix.composeUser && (
        <>
          <p className={styles.fixText}>{t('storage.fix.user')}</p>
          <CopyBlock text={fix.composeUser} />
          {fix.configChown && (
            <>
              <p className={styles.fixText}>{t('storage.fix.userConfig')}</p>
              <CopyBlock text={fix.configChown} />
            </>
          )}
          <p className={styles.fixText}>{t('storage.fix.userApply')}</p>
          <CopyBlock text={apply} />
        </>
      )}
      {fix.mount && fix.mount.length > 0 && (
        <>
          <p className={styles.fixText}>{t('storage.fix.mount')}</p>
          {fix.mount.map((p) => (
            <p key={p} className={`${styles.fixText} ${styles.mono}`}>
              {p}
            </p>
          ))}
          <p className={styles.fixText}>{t('storage.fix.userApply')}</p>
          <CopyBlock text={apply} />
        </>
      )}
      {whys.has('readonlyFS') && (
        <>
          <p className={styles.fixText}>{t('storage.fix.readonly')}</p>
          <CopyBlock text={apply} />
        </>
      )}
      {whys.has('other') && <p className={styles.fixText}>{t('storage.fix.other')}</p>}
    </Notice>
  )
}

export function StoragePerms({ view, title, footer }: { view: StorageView; title?: string; footer?: string }) {
  const t = useT()
  const rows = checked(view, t)
  const bad = rows.some((r) => hasProblem(r.d))
  // no media folder: the volume is missing, and what is below it cannot be fixed by an owner
  const rootGone = view.root.status === 'missing' || view.root.status === 'notdir'
  return (
    <>
      <GroupedSection title={title} footer={footer}>
        {rows.map((r) => (
          <ListRow
            key={r.key}
            label={r.label}
            hint={<span className={styles.mono}>{r.path}</span>}
            status={<span className={hasProblem(r.d) ? styles.problem : r.d.status === 'missing' ? styles.neutral : styles.fine}>{permText(r.d, t)}</span>}
          />
        ))}
      </GroupedSection>
      {rootGone ? (
        <Notice tone="destructive">{t('storage.rootMissing', { root: view.root.path })}</Notice>
      ) : bad ? (
        <Fix view={view} />
      ) : (
        <p className={styles.fixText}>{t('storage.perms.allGood')}</p>
      )}
    </>
  )
}
