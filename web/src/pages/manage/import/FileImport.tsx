// The way in when the server has no sign-in services (the core build, a
// library-only server): one card, «Загрузить файл» — a CSV, M3U, Library.xml
// or a Google Takeout archive. The sources build shows ./Services instead.
import { useRef } from 'react'
import { Button } from '../../../ui/Button'
import { Icon } from '../../../components/Icon'
import { useT } from '../../../i18n'
import { IMPORT_ACCEPT, serviceName } from './serviceName'
import styles from './ServiceTiles.module.css'

export function FileImport({ onFiles, busy }: { onFiles: (files: File[]) => void; busy?: boolean }) {
  const t = useT()
  const input = useRef<HTMLInputElement>(null)
  return (
    <>
      <input
        ref={input}
        type="file"
        multiple
        hidden
        accept={IMPORT_ACCEPT}
        aria-label={t('import.tiles.fileAria')}
        onChange={(e) => {
          const files = Array.from(e.target.files ?? [])
          e.target.value = '' // the same file can be picked again
          if (files.length) onFiles(files)
        }}
      />
      <ul className={styles.tiles} aria-label={t('import.tiles.servicesAria')}>
        <li className={styles.tile} data-testid="import-tile">
          <div className={styles.head}>
            <span className={styles.icon} style={{ background: '#64d2ff' }} aria-hidden>
              <Icon name="folder" size={20} strokeWidth={2.2} />
            </span>
            <h3 className={styles.name}>{serviceName('file')}</h3>
          </div>
          <p className={styles.what}>{t('import.look.file')}</p>
          <div className={styles.actions}>
            <Button size="small" icon="folder" loading={busy} disabled={busy} onClick={() => input.current?.click()}>
              {t('import.tiles.upload')}
            </Button>
          </div>
        </li>
      </ul>
    </>
  )
}
