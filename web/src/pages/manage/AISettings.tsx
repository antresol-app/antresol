// «Настройки сервера → ИИ-помощник»: the language models Antresol's AI
// tasks ask. One endpoint, three models by weight — light (many quick
// calls: naming TV categories), medium («Опишите канал»), powerful (the
// hardest tasks) — and each task asks its own tier. A provider fills its
// address and models to start from: a LiteLLM (or any OpenAI-compatible
// server), Anthropic Claude (its own Messages API; the tiers are pop-ups
// of Haiku, Sonnet and Opus), Gemini, OpenAI, Groq, OpenRouter or an
// Ollama. The key is written, never shown again; «Проверить» asks each
// model one short question. Without one the channel is read from the
// words alone.
import { useRef, useState } from 'react'
import { testAISettings, errorText } from '../../api/client'
import type { AISettings as AI, AIModels } from '../../api/types'
import { useT } from '../../i18n'
import { Button } from '../../ui/Button'
import { GroupedSection, ListRow } from '../../ui/List'
import { Select } from '../../ui/Select'
import { TextField } from '../../ui/TextField'
import styles from './Settings.module.css'

type Provider = 'off' | 'litellm' | 'anthropic' | 'gemini' | 'openai' | 'groq' | 'openrouter' | 'ollama'
type Tier = keyof AIModels
const TIERS: Tier[] = ['light', 'medium', 'powerful']

const same = (m: string): AIModels => ({ light: m, medium: m, powerful: m })

export const AI_PROVIDERS: Record<Exclude<Provider, 'off' | 'litellm'>, { url: string; models: AIModels }> = {
  anthropic: { url: 'https://api.anthropic.com', models: { light: 'claude-haiku-5-5', medium: 'claude-sonnet-5-5', powerful: 'claude-opus-5-5' } },
  gemini: { url: 'https://generativelanguage.googleapis.com/v1beta/openai', models: same('gemini-3.5-flash-lite') },
  openai: { url: 'https://api.openai.com/v1', models: same('gpt-5-mini') },
  groq: { url: 'https://api.groq.com/openai/v1', models: same('openai/gpt-oss-120b') },
  openrouter: { url: 'https://openrouter.ai/api/v1', models: same('google/gemini-3.5-flash-lite') },
  ollama: { url: 'http://localhost:11434', models: same('qwen3.5:2b') },
}

/** Anthropic's models the tiers pick from. */
const CLAUDE_MODELS = ['claude-haiku-5-5', 'claude-sonnet-5-5', 'claude-opus-5-5']

/** The provider an address is of (a LiteLLM or any other server: «litellm»). */
export function providerOf(url: string): Provider {
  if (!url) return 'off'
  for (const [p, v] of Object.entries(AI_PROVIDERS)) if (p !== 'ollama' && url.startsWith(v.url)) return p as Provider
  if (/:11434(\/|$)/.test(url)) return 'ollama'
  return 'litellm'
}

/** The tiers of stored settings (a server of one model: that model on every tier). */
export function modelsOf(ai?: AI): AIModels {
  const m = ai?.models
  if (m && (m.light || m.medium || m.powerful)) return { light: m.light || m.medium, medium: m.medium || m.light, powerful: m.powerful || m.medium }
  return same(ai?.model ?? '')
}

type Draft = { url: string; models: AIModels; key: string }
const toAI = (d: Draft): AI => ({ url: d.url, model: d.models.medium, models: d.models, key: d.key })

export function AISettingsSection({ ai, keySet, fromEnv, onChange }: { ai?: AI; keySet: boolean; fromEnv?: string; onChange: (ai: AI) => void }) {
  const t = useT()
  const [draft, setDraft] = useState<Draft>({ url: ai?.url ?? '', models: modelsOf(ai), key: '' })
  const [provider, setProvider] = useState<Provider>(providerOf(ai?.url ?? ''))
  const saved = useRef<Draft>({ ...draft })
  const [test, setTest] = useState<{ busy: boolean; text: string; ok?: boolean }>({ busy: false, text: '' })
  const commit = (next: Draft = draft) => {
    if (JSON.stringify({ ...saved.current, key: '' }) === JSON.stringify({ ...next, key: '' }) && !next.key) return
    saved.current = { ...next, key: '' }
    onChange(toAI(next))
  }
  const field = (patch: Partial<Draft>) => setDraft((d) => ({ ...d, ...patch }))
  const setTier = (tier: Tier, model: string) => field({ models: { ...draft.models, [tier]: model } })
  const choose = (p: Provider) => {
    setProvider(p)
    setTest({ busy: false, text: '' })
    const next: Draft =
      p === 'off'
        ? { url: '', models: same(''), key: '' }
        : p === 'litellm'
          ? { url: draft.url && providerOf(draft.url) === 'litellm' ? draft.url : 'http://127.0.0.1:4000/v1', models: draft.models, key: '' }
          : { url: AI_PROVIDERS[p].url, models: { ...AI_PROVIDERS[p].models }, key: '' }
    setDraft(next)
    commit(next)
  }
  const run = async () => {
    setTest({ busy: true, text: '' })
    try {
      const r = await testAISettings({ url: draft.url, model: draft.models.medium, models: draft.models, key: draft.key || undefined })
      const failed = r.tiers?.find((x) => !x.ok)
      setTest({
        busy: false,
        ok: r.ok,
        text: r.ok
          ? t('settings.ai.testOk', { ms: r.ms })
          : failed
            ? t('settings.ai.testTierFailed', { tier: t(`settings.ai.tier.${failed.tier}`), error: failed.error ?? '' })
            : t('settings.ai.testFailed', { error: r.error ?? '' }),
      })
    } catch (e) {
      setTest({ busy: false, ok: false, text: errorText(e, t('settings.ai.testFailedShort')) })
    }
  }
  const on = provider !== 'off'
  const claude = provider === 'anthropic'
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
            { value: 'anthropic', label: 'Anthropic Claude' },
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
            {!claude &&
              TIERS.map((tier) => (
                <TextField
                  key={tier}
                  label={t(`settings.ai.tier.${tier}`)}
                  hint={t(`settings.ai.tierHint.${tier}`)}
                  autoComplete="off"
                  spellCheck={false}
                  value={draft.models[tier]}
                  onChange={(e) => setTier(tier, e.target.value)}
                  onBlur={() => commit()}
                />
              ))}
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
      {on &&
        claude &&
        TIERS.map((tier) => (
          <ListRow key={tier} label={t(`settings.ai.tier.${tier}`)} hint={t(`settings.ai.tierHint.${tier}`)}>
            <Select
              label={t(`settings.ai.tier.${tier}`)}
              placement="bottom-end"
              value={draft.models[tier]}
              options={[...new Set([...CLAUDE_MODELS, draft.models[tier]])].filter(Boolean).map((m) => ({ value: m, label: m }))}
              onChange={(m: string) => {
                const next = { ...draft, models: { ...draft.models, [tier]: m } }
                setDraft(next)
                commit(next)
              }}
            />
          </ListRow>
        ))}
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
