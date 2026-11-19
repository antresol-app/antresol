// «Адрес сервера» (Настройки сервера): the address the server is reached at
// from outside the home network (a domain behind a reverse proxy or a
// tunnel). The links of letters, Shikimori's redirect URI and the apps away
// from home are on it; it applies at once. VAULT_PUBLIC_URL, when the server
// has it, is the address whatever is saved: the field shows it, read-only.
// The places that need the address (Shikimori, the letters) link here.
import { useEffect, useState } from 'react'
import type { Settings as Values } from '../../api/types'
import { useT } from '../../i18n'
import { GroupedSection, ListRow } from '../../ui/List'
import { TextField } from '../../ui/TextField'
import { DOCS_URL } from '../../brand'
import styles from './Settings.module.css'

/** Where «Адрес сервера» is: the places that need the address link here. */
export const PUBLIC_ADDRESS_PATH = '/manage/settings/address'

export function PublicAddressSection({ values, onChange }: { values: Values; onChange: (patch: Partial<Values>) => void }) {
  const t = useT()
  const env = values.publicUrlFromEnv ?? ''
  const [text, setText] = useState(values.publicUrl ?? '')
  // the server's answer (trimmed, without the trailing slash) replaces what was typed
  useEffect(() => setText(values.publicUrl ?? ''), [values.publicUrl])
  const commit = () => {
    const v = text.trim()
    if (v !== (values.publicUrl ?? '')) onChange({ publicUrl: v })
  }
  return (
    <GroupedSection
      footer={
        <>
          {t('settings.address.footer')}{' '}
          <a href={`${DOCS_URL}configuration.html`} target="_blank" rel="noreferrer">
            {t('settings.address.docs')}
          </a>
        </>
      }
    >
      <ListRow wide label={t('settings.address')} hint={t('settings.address.hint')} status={env ? t('settings.address.fromEnv') : undefined}>
        <div className={styles.aiFields}>
          <TextField
            label={t('settings.address.field')}
            hideLabel
            value={env || text}
            readOnly={Boolean(env)}
            placeholder="https://media.example.org"
            onChange={(e) => setText(e.target.value)}
            onBlur={commit}
            onKeyDown={(e) => e.key === 'Enter' && commit()}
            inputMode="url"
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            autoComplete="off"
          />
        </div>
      </ListRow>
    </GroupedSection>
  )
}
