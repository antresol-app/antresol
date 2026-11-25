// The way in when the server has no sign-in services (the core build, a
// library-only server): one row, «Загрузить файл» — a CSV, M3U, Library.xml
// or a Google Takeout archive — with the CSV's columns a «Формат CSV» away.
// The sources build shows ./Services instead.
import { useRef, useState } from 'react'
import { Button } from '../../../ui/Button'
import { FileInput } from '../../../ui/FileInput'
import { GroupedSection, ListRow } from '../../../ui/List'
import { SettingsIcon } from '../../../ui/SettingsIcon'
import { Sheet } from '../../../ui/Sheet'
import { useT } from '../../../i18n'
import { IMPORT_ACCEPT, serviceName } from './serviceName'

export function FileImport({ onFiles, busy }: { onFiles: (files: File[]) => void; busy?: boolean }) {
  const t = useT()
  const input = useRef<HTMLInputElement>(null)
  const [format, setFormat] = useState(false)
  return (
    <>
      <FileInput
        ref={input}
        multiple
        accept={IMPORT_ACCEPT}
        aria-label={t('import.tiles.fileAria')}
        onChange={(e) => {
          const files = Array.from(e.target.files ?? [])
          e.target.value = '' // the same file can be picked again
          if (files.length) onFiles(files)
        }}
      />
      <GroupedSection
        footer={
          <Button variant="link" onClick={() => setFormat(true)}>
            {t('import.csv.title')}
          </Button>
        }
      >
        <ListRow leading={<SettingsIcon glyph="folder" tint="teal" />} label={serviceName('file')} hint={t('import.file.what')}>
          <Button size="small" icon="folder" loading={busy} disabled={busy} onClick={() => input.current?.click()}>
            {t('import.tiles.upload')}
          </Button>
        </ListRow>
      </GroupedSection>
      {format && (
        <Sheet
          title={t('import.csv.title')}
          message={t('import.csv.text')}
          onCancel={() => setFormat(false)}
          actions={[{ label: t('common.done'), kind: 'primary', run: () => setFormat(false) }]}
        />
      )}
    </>
  )
}
