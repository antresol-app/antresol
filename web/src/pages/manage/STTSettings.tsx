// «Настройки сервера → ИИ-помощник → Распознавание речи»: the service that
// makes the audiobooks' transcripts. The one pop-up is the choice:
// «Выключено», the server's own («Сервер — модель», when it has
// VAULT_STT_*), Groq, OpenAI, Gemini (their own addresses and usual models)
// or a server of one's own (a LiteLLM, a Whisper server: its address and
// model, saved once both are there). The key is written, never shown again,
// and kept only for the address it was saved with; «Проверить» sends a
// second of silence.
import { useRef, useState } from 'react'
import { errorText, testSTTSettings } from '../../api/client'
import type { STTSettings as STT } from '../../api/types'
import { useT } from '../../i18n'
import { GroupedSection, ListRow, ListSelectRow } from '../../ui/List'
import { Select } from '../../ui/Select'
import { TextField } from '../../ui/TextField'
import styles from './Settings.module.css'

type Choice = 'off' | 'server' | 'groq' | 'openai' | 'gemini' | 'custom'

/** The model each provider is asked when none is typed (the server's own defaults). */
const USUAL_MODELS: Partial<Record<Choice, string>> = {
  groq: 'whisper-large-v3-turbo',
  openai: 'whisper-1',
  gemini: 'gemini-3.5-flash-lite',
}

/** What the section shows chosen: the stored provider; none stored: the server's own when it has one. */
export function chosenSTT(stt: STT | undefined, fromEnv: string | undefined): Choice {
  const p = stt?.provider
  if (p === 'groq' || p === 'openai' || p === 'gemini' || p === 'custom') return p
  return p === 'off' || !fromEnv ? 'off' : 'server'
}

type Draft = Required<STT>

export function STTSettingsSection({ stt, keySet, fromEnv, onChange }: { stt?: STT; keySet: boolean; fromEnv?: string; onChange: (stt: STT) => void }) {
  const t = useT()
  const [choice, setChoice] = useState<Choice>(chosenSTT(stt, fromEnv))
  const [draft, setDraft] = useState<Draft>({ provider: stt?.provider ?? '', url: stt?.url ?? '', model: stt?.model ?? '', key: '' })
  const saved = useRef<Draft>({ ...draft })
  const [test, setTest] = useState<{ busy: boolean; text: string }>({ busy: false, text: '' })
  const commit = (next: Draft = draft) => {
    // a server of one's own needs both before there is anything to save (a
    // key typed first waits in its field)
    if (next.provider === 'custom' && !(next.url.trim() && next.model.trim())) return
    if (JSON.stringify(saved.current) === JSON.stringify(next)) return
    saved.current = { ...next, key: '' }
    onChange(next)
    // a key is written once, never shown again
    if (next.key) setDraft({ ...next, key: '' })
  }
  const field = (patch: Partial<Draft>) => setDraft((d) => ({ ...d, ...patch }))
  const choose = (c: Choice) => {
    setChoice(c)
    setTest({ busy: false, text: '' })
    const next: Draft = { provider: c === 'server' ? '' : c, url: '', model: '', key: '' }
    setDraft(next)
    commit(next)
  }
  // the stored key goes with the provider and address it was saved with
  const keyKept = keySet && stt?.provider === draft.provider && (draft.provider !== 'custom' || stt?.url === draft.url.replace(/\/+$/, ''))
  const run = async () => {
    setTest({ busy: true, text: '' })
    try {
      const r = await testSTTSettings({ provider: draft.provider, url: draft.url, model: draft.model, key: draft.key || undefined })
      setTest({ busy: false, text: r.ok ? t('settings.stt.ok', { ms: r.ms }) : t('settings.stt.failed', { error: r.error ?? '' }) })
    } catch (e) {
      setTest({ busy: false, text: errorText(e, t('settings.stt.checkFailed')) })
    }
  }
  const custom = choice !== 'off' && choice !== 'server'
  return (
    <GroupedSection id="stt-title" title={t('settings.stt.title')} footer={t('settings.stt.footer')}>
      <ListRow label={t('settings.stt.provider')}>
        <Select
          label={t('settings.stt.provider')}
          placement="bottom-end"
          value={choice}
          options={[
            { value: 'off', label: t('settings.stt.off') },
            ...(fromEnv ? [{ value: 'server' as const, label: t('settings.stt.server', { model: fromEnv }) }] : []),
            { value: 'groq', label: 'Groq' },
            { value: 'openai', label: 'OpenAI' },
            { value: 'gemini', label: 'Google Gemini' },
            { value: 'custom', label: t('settings.ai.litellm') },
          ]}
          onChange={choose}
        />
      </ListRow>
      {custom && (
        <ListRow label={t('settings.ai.connection')} wide>
          <div className={styles.aiFields}>
            {choice === 'custom' && (
              <TextField label={t('settings.ai.url')} placeholder="http://127.0.0.1:4000/v1" autoComplete="off" spellCheck={false} inputMode="url" value={draft.url} onChange={(e) => field({ url: e.target.value })} onBlur={() => commit()} />
            )}
            <TextField label={t('settings.stt.model')} placeholder={USUAL_MODELS[choice]} autoComplete="off" spellCheck={false} value={draft.model} onChange={(e) => field({ model: e.target.value })} onBlur={() => commit()} />
            <TextField
              label={t('settings.ai.key')}
              placeholder={keyKept ? t('settings.ai.keySaved') : undefined}
              type="password"
              revealable
              autoComplete="new-password"
              value={draft.key}
              onChange={(e) => field({ key: e.target.value })}
              onBlur={() => commit()}
            />
          </div>
        </ListRow>
      )}
      {choice !== 'off' && (
        <ListSelectRow
          tone="accent"
          label={test.busy ? t('settings.stt.checking') : t('settings.stt.check')}
          hint={test.text ? <span role="status">{test.text}</span> : undefined}
          disabled={test.busy}
          onSelect={() => void run()}
        />
      )}
    </GroupedSection>
  )
}
