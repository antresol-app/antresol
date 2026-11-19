// «Настройки сервера → Медиатека → Папки на сервере»: the checks of the
// first-run wizard, kept for later. Each library's folders (several per
// kind, added from the folder browser over what is mounted into the server,
// marked «Только для взрослых» or «Скрыть из общих подборок»; a change is
// checked at once and takes effect when applied: the server restarts for a
// few seconds for other folders, marks apply at once; taking out a folder
// asks first, its titles leave the library), and the write test with
// «Проверить» and the fix to copy when the server may not write: each
// library folder's row says whether writing works, the media folder and the
// transcoding folder have their own rows below (never a second list of the
// same folders).
// Its own endpoint (/api/settings/storage), not the settings object: until
// folders are picked here the server's VAULT_*_DIR apply.
import { useState } from 'react'
import { useT } from '../../i18n'
import { Button } from '../../ui/Button'
import { GroupedSection, ListRow } from '../../ui/List'
import { Notice } from '../../ui/Notice'
import { RemoveConfirm, StorageFolders } from '../../components/storage/StorageFolders'
import { StoragePerms } from '../../components/storage/StoragePerms'
import { useStorage } from '../../components/storage/useStorage'
import m from './Manage.module.css'

export function StorageSection() {
  const t = useT()
  const storage = useStorage()
  const [saved, setSaved] = useState(false)
  const [manual, setManual] = useState(false)
  const [asking, setAsking] = useState<null | { reset: boolean }>(null)
  const { view } = storage

  if (!view) {
    return (
      <GroupedSection title={t('storage.title')}>
        <ListRow label={t('storage.title')}>
          {storage.error ? (
            <span className={m.problem} role="alert">
              {storage.error}
            </span>
          ) : (
            <span className={m.spinnerRow} role="status">
              <span className={m.spinner} aria-hidden />
            </span>
          )}
        </ListRow>
      </GroupedSection>
    )
  }

  const apply = async (reset: boolean, confirm = false) => {
    // titles would leave the library: ask first
    if (!confirm && !reset && storage.removals.length > 0) {
      setAsking({ reset })
      return
    }
    setAsking(null)
    setSaved(false)
    const moves = reset || storage.moves
    const ok = await storage.save(reset, confirm)
    if (ok) {
      setSaved(true)
      setManual(!view.restarts && moves)
    }
  }
  const disabled = storage.busy || storage.restarting
  return (
    <>
      {storage.restarting && <Notice tone="accent">{t('wizard.restarting')}</Notice>}
      {storage.slow && <Notice tone="warning">{t('wizard.restartSlow')}</Notice>}
      {storage.error && (
        <div className={m.problem} role="alert">
          {storage.error}
        </div>
      )}
      <GroupedSection title={t('storage.title')} footer={t('storage.settings.footer')}>
        {storage.dirty && (
          <ListRow label={view.restarts && storage.moves ? t('storage.apply') : t('storage.applyManual')}>
            <Button size="small" loading={storage.busy} disabled={disabled} onClick={() => void apply(false)}>
              {view.restarts && storage.moves ? t('storage.apply') : t('storage.applyManual')}
            </Button>
          </ListRow>
        )}
        {view.custom && !storage.dirty && (
          <ListRow label={t('storage.reset')}>
            <Button size="small" variant="secondary" disabled={disabled} onClick={() => void apply(true)}>
              {t('storage.reset')}
            </Button>
          </ListRow>
        )}
        {saved && !storage.dirty && !manual && <ListRow label={t('storage.saved')} />}
        {manual && <ListRow label={t('storage.restartManual')} />}
        <ListRow label={t('storage.perms')} hint={t('storage.perms.hint')}>
          <Button size="small" variant="secondary" icon="restart" loading={storage.busy && !storage.restarting} disabled={disabled} onClick={() => void storage.recheck()}>
            {storage.busy && !storage.restarting ? t('storage.checking') : t('storage.check')}
          </Button>
        </ListRow>
      </GroupedSection>
      <StorageFolders storage={storage} entries={false} />
      {(asking || storage.confirmNeeded) && (
        <RemoveConfirm
          removals={storage.removals}
          busy={storage.busy}
          onCancel={() => {
            setAsking(null)
            storage.dismissConfirm()
          }}
          onConfirm={() => void apply(asking?.reset ?? false, true)}
        />
      )}
      {/* the library's folders say their write test in their rows above: only the others here */}
      <StoragePerms view={view} libraryFolders={false} title={t('storage.perms.serverFolders')} />
    </>
  )
}
