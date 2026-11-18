// «Настройки сервера → Медиатека → Папки на сервере»: the checks of the
// first-run wizard, kept for later. What the server sees under the media
// folder, which folder is which library (a pick is checked at once and takes
// effect when applied: the server restarts for a few seconds), and the write
// test with «Проверить» and the fix to copy when the server may not write.
// Its own endpoint (/api/settings/storage), not the settings object: until
// folders are picked here the server's VAULT_*_DIR apply.
import { useState } from 'react'
import { useT } from '../../i18n'
import { Button } from '../../ui/Button'
import { GroupedSection, ListRow } from '../../ui/List'
import { Notice } from '../../ui/Notice'
import { StorageFolders } from '../../components/storage/StorageFolders'
import { StoragePerms } from '../../components/storage/StoragePerms'
import { useStorage } from '../../components/storage/useStorage'
import m from './Manage.module.css'

export function StorageSection() {
  const t = useT()
  const storage = useStorage()
  const [saved, setSaved] = useState(false)
  const [manual, setManual] = useState(false)
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

  const apply = async (reset: boolean) => {
    setSaved(false)
    const ok = await storage.save(reset)
    if (ok) {
      setSaved(true)
      setManual(!view.restarts)
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
      <StorageFolders storage={storage} title={t('storage.title')} entries={false} />
      <GroupedSection footer={t('storage.settings.footer')}>
        {storage.dirty && (
          <ListRow label={view.restarts ? t('storage.apply') : t('storage.applyManual')}>
            <Button size="small" loading={storage.busy} disabled={disabled} onClick={() => void apply(false)}>
              {view.restarts ? t('storage.apply') : t('storage.applyManual')}
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
      <StoragePerms view={view} />
    </>
  )
}
