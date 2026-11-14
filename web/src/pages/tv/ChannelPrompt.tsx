// «Опишите канал» at the top of the channel editor: a sentence («советские
// комедии 70-х») read by the server into the channel's tags and name
// (POST /api/channels/suggest — its words, and a language model when it
// has one). Nothing is saved: the tags below show what was understood.
import { useState } from 'react'
import { suggestChannel, type ChannelSuggestion } from '../../api/channels'
import { errorText } from '../../api/client'
import { useT } from '../../i18n'
import { Button } from '../../ui/Button'
import { GroupedSection } from '../../ui/List'
import { TextField } from '../../ui/TextField'
import styles from './Tv.module.css'

export function ChannelPrompt({ onSuggested }: { onSuggested: (s: ChannelSuggestion) => void }) {
  const t = useT()
  const [text, setText] = useState('')
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')
  const [error, setError] = useState('')
  const run = async () => {
    if (!text.trim() || busy) return
    setBusy(true)
    setNote('')
    setError('')
    try {
      const s = await suggestChannel(text.trim())
      const r = s.rules
      const understood = Boolean(r.kinds?.length || r.genres?.length || r.countries?.length || r.languages?.length || r.keywords?.length || r.yearFrom || r.yearTo)
      onSuggested(s)
      setNote(!understood ? t('tv.ai.nothing') : s.via === 'ai' ? t('tv.ai.doneAI') : t('tv.ai.done'))
    } catch (e) {
      setError(errorText(e, t('tv.ai.failed')))
    } finally {
      setBusy(false)
    }
  }
  return (
    <GroupedSection>
      <li className={styles.prompt}>
        <form
          className={styles.promptRow}
          onSubmit={(e) => {
            e.preventDefault()
            void run()
          }}
        >
          <TextField
            label={t('tv.ai.label')}
            placeholder={t('tv.ai.placeholder')}
            value={text}
            onChange={(e) => setText(e.target.value)}
            autoComplete="off"
            maxLength={300}
          />
          <Button type="submit" variant="primary" icon="sparkles" loading={busy} disabled={!text.trim() || busy}>
            {busy ? t('tv.ai.building') : t('tv.ai.build')}
          </Button>
        </form>
        {(note || error) && (
          <p className={error ? styles.formError : styles.promptNote} role={error ? 'alert' : 'status'}>
            {error || note}
          </p>
        )}
      </li>
    </GroupedSection>
  )
}
