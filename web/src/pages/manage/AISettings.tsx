// «Настройки сервера → ИИ-помощник»: the language model «Опишите канал»
// asks. A provider fills its address and a model to start from: a LiteLLM
// (or any OpenAI-compatible server), Gemini, OpenAI, Groq, OpenRouter or an
// Ollama. The key is written, never shown again; «Проверить» asks the model
// one short question. Without one the channel is read from the words alone.
import { useRef, useState } from 'react'
import { testAISettings, errorText } from '../../api/client'
import type { AISettings as AI } from '../../api/types'
import { useT } from '../../i18n'
import { Button } from '../../ui/Button'
import { GroupedSection, ListRow } from '../../ui/List'
import { Select } from '../../ui/Select'
import { TextField } from '../../ui/TextField'
import styles from './Settings.module.css'

type Provider = 'off' | 'litellm' | 'gemini' | 'openai' | 'groq' | 'openrouter' | 'ollama'

export const AI_PROVIDERS: Record<Exclude<Provider, 'off' | 'litellm'>, { url: string; model: string }> = {
  gemini: { url: 'https://generativelanguage.googleapis.com/v1beta/openai', model: 'gemini-3.5-flash-lite' },
  openai: { url: 'https://api.openai.com/v1', model: 'gpt-5-mini' },
  groq: { url: 'https://api.groq.com/openai/v1', model: 'openai/gpt-oss-120b' },
  openrouter: { url: 'https://openrouter.ai/api/v1', model: 'google/gemini-3.5-flash-lite' },
  ollama: { url: 'http://localhost:11434', model: 'qwen3.5:2b' },
}

/** The provider an address is of (a LiteLLM or any other server: «litellm»). */
export function providerOf(url: string): Provider {
  if (!url) return 'off'
  for (const [p, v] of Object.entries(AI_PROVIDERS)) if (p !== 'ollama' && url.startsWith(v.url)) return p as Provider
  if (/:11434(\/|$)/.test(url)) return 'ollama'
  return 'litellm'
}

export function AISettingsSection({ ai, keySet, fromEnv, onChange }: { ai?: AI; keySet: boolean; fromEnv?: string; onChange: (ai: AI) => void }) {
  const t = useT()
  const [draft, setDraft] = useState<AI>({ url: ai?.url ?? '', model: ai?.model ?? '', key: '' })
  const [provider, setProvider] = useState<Provider>(providerOf(ai?.url ?? ''))
  const saved = useRef<AI>({ ...draft })
  const [test, setTest] = useState<{ busy: boolean; text: string; ok?: boolean }>({ busy: false, text: '' })
  const commit = (next: AI = draft) => {
    if (JSON.stringify({ ...saved.current, key: '' }) === JSON.stringify({ ...next, key: '' }) && !next.key) return
    saved.current = { ...next, key: '' }
    onChange(next)
  }
  const field = (patch: Partial<AI>) => setDraft((d) => ({ ...d, ...patch }))
  const choose = (p: Provider) => {
    setProvider(p)
    setTest({ busy: false, text: '' })
    const next: AI =
      p === 'off' ? { url: '', model: '', key: '' } : p === 'litellm' ? { url: draft.url && providerOf(draft.url) === 'litellm' ? draft.url : 'http://127.0.0.1:4000/v1', model: draft.model, key: '' } : { ...AI_PROVIDERS[p], key: '' }
    setDraft(next)
    commit(next)
  }
  const run = async () => {
    setTest({ busy: true, text: '' })
    try {
      const r = await testAISettings({ url: draft.url, model: draft.model, key: draft.key || undefined })
      setTest({ busy: false, ok: r.ok, text: r.ok ? t('settings.ai.testOk', { ms: r.ms }) : t('settings.ai.testFailed', { error: r.error ?? '' }) })
    } catch (e) {
      setTest({ busy: false, ok: false, text: errorText(e, t('settings.ai.testFailedShort')) })
    }
  }
  const on = provider !== 'off'
  return (
    <GroupedSection footer={!on && fromEnv ? t('settings.ai.fromEnv', { model: fromEnv }) : t('settings.ai.footer')}>
      <ListRow label={t('settings.ai.provider')}>
        <Select
          label={t('settings.ai.provider')}
          placement="bottom-end"
          value={provider}
          options={[
            { value: 'off', label: t('settings.ai.off') },
            { value: 'litellm', label: t('settings.ai.litellm') },
            { value: 'gemini', label: 'Google Gemini' },
            { value: 'openai', label: 'OpenAI' },
            { value: 'groq', label: 'Groq' },
            { value: 'openrouter', label: 'OpenRouter' },
            { value: 'ollama', label: 'Ollama' },
          ]}
          onChange={choose}
        />
      </ListRow>
      {on && (
        <ListRow label={t('settings.ai.connection')} wide>
          <div className={styles.aiFields}>
            <TextField label={t('settings.ai.url')} autoComplete="off" spellCheck={false} inputMode="url" value={draft.url} onChange={(e) => field({ url: e.target.value })} onBlur={() => commit()} />
            <TextField label={t('settings.ai.model')} autoComplete="off" spellCheck={false} value={draft.model} onChange={(e) => field({ model: e.target.value })} onBlur={() => commit()} />
            {provider !== 'ollama' && (
              <TextField
                label={t('settings.ai.key')}
                hint={keySet ? t('settings.ai.keySet') : t('settings.ai.keyHint')}
                type="password"
                revealable
                autoComplete="new-password"
                value={draft.key ?? ''}
                onChange={(e) => field({ key: e.target.value })}
                onBlur={() => {
                  if (!draft.key) return
                  commit()
                  field({ key: '' })
                }}
              />
            )}
          </div>
        </ListRow>
      )}
      {(on || fromEnv) && (
        <ListRow label={t('settings.ai.test')} status={test.text || undefined}>
          <Button size="small" variant="secondary" icon="sparkles" loading={test.busy} disabled={test.busy} onClick={() => void run()}>
            {t('settings.ai.testButton')}
          </Button>
        </ListRow>
      )}
    </GroupedSection>
  )
}
