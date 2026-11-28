// «ИИ-помощник»: what the page knows about a list's entry — its provider
// (by the address), the words of its status, the line under its name.
import type { AIModel, AIModels, AIStatus, AITier } from '../../api/types'
import type { TFunction } from '../../i18n'

export type Provider = 'litellm' | 'anthropic' | 'gemini' | 'openai' | 'groq' | 'openrouter' | 'ollama'

const same = (m: string): AIModels => ({ light: m, medium: m, powerful: m })

/** Each provider's address and the model it starts from. */
export const AI_PROVIDERS: Record<Exclude<Provider, 'litellm'>, { url: string; models: AIModels }> = {
  anthropic: { url: 'https://api.anthropic.com', models: { light: 'claude-haiku-5-5', medium: 'claude-sonnet-5-5', powerful: 'claude-opus-5-5' } },
  gemini: { url: 'https://generativelanguage.googleapis.com/v1beta/openai', models: same('gemini-3.5-flash-lite') },
  openai: { url: 'https://api.openai.com/v1', models: same('gpt-5-mini') },
  groq: { url: 'https://api.groq.com/openai/v1', models: same('openai/gpt-oss-120b') },
  openrouter: { url: 'https://openrouter.ai/api/v1', models: same('google/gemini-3.5-flash-lite') },
  ollama: { url: 'http://localhost:11434', models: same('qwen3.5:2b') },
}

/** What «LiteLLM или свой сервер» starts from. */
export const LITELLM_URL = 'http://127.0.0.1:4000/v1'

export const PROVIDER_NAMES: Record<Provider, string> = {
  litellm: 'LiteLLM',
  anthropic: 'Anthropic',
  gemini: 'Gemini',
  openai: 'OpenAI',
  groq: 'Groq',
  openrouter: 'OpenRouter',
  ollama: 'Ollama',
}

export const TIERS: AITier[] = ['light', 'medium', 'powerful']

/** The provider an address is of (a LiteLLM or any other server: «litellm»). */
export function providerOf(url: string): Provider {
  for (const [p, v] of Object.entries(AI_PROVIDERS)) if (p !== 'ollama' && url.startsWith(v.url)) return p as Provider
  if (/:11434(\/|$)/.test(url)) return 'ollama'
  return 'litellm'
}

/** A provider that asks for a key (the others — a LiteLLM, an own server, an Ollama — may have none). */
export const needsKey = (url: string) => providerOf(url) !== 'litellm' && providerOf(url) !== 'ollama'

export function hostOf(url: string): string {
  try {
    return new URL(url).host.toLowerCase()
  } catch {
    return ''
  }
}

/** The address is http:// or https:// with a host. */
export function validUrl(url: string): boolean {
  try {
    const u = new URL(url)
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.host !== ''
  } catch {
    return false
  }
}

export type StatusTone = 'success' | 'neutral' | 'warning' | 'destructive'

/** The short word of a status (a row's trailing value) and its tone. */
export function statusView(t: TFunction, st: AIStatus | undefined): { text: string; tone: StatusTone } {
  switch (st?.state) {
    case 'ok':
      return { text: t('settings.ai.st.ok', { ms: st.ms ?? 0 }), tone: 'success' }
    case 'error':
      if (st.kind === 'http' && st.code) return { text: t('settings.ai.st.error', { code: st.code }), tone: 'destructive' }
      if (st.kind === 'timeout') return { text: t('settings.ai.st.timeout'), tone: 'destructive' }
      if (st.kind === 'network') return { text: t('settings.ai.st.network'), tone: 'destructive' }
      if (st.kind === 'answer') return { text: t('settings.ai.st.answer'), tone: 'destructive' }
      if (st.kind === 'key') return { text: t('settings.ai.st.noKey'), tone: 'warning' }
      return { text: t('settings.ai.st.errorOther'), tone: 'destructive' }
    case 'noKey':
      return { text: t('settings.ai.st.noKey'), tone: 'warning' }
    case 'off':
      return { text: t('settings.ai.st.off'), tone: 'neutral' }
    default:
      return { text: t('settings.ai.st.unchecked'), tone: 'neutral' }
  }
}

/** The full account of a status (the entry's sheet): what happened, in words, then the provider's own. */
export function statusDetail(t: TFunction, st: AIStatus | undefined): string {
  switch (st?.state) {
    case 'ok':
      return t('settings.ai.stDetail.ok', { ms: st.ms ?? 0 })
    case 'error': {
      // a provider's answer begins with its code; the other failures get a word of ours
      if (st.kind === 'http' && st.error) return st.error
      const word = st.kind === 'timeout' ? t('settings.ai.stDetail.timeout') : st.kind === 'network' ? t('settings.ai.stDetail.network') : st.kind === 'answer' ? t('settings.ai.stDetail.answer') : st.kind === 'key' ? t('settings.ai.stDetail.noKey') : t('settings.ai.st.errorOther')
      return st.error ? `${word}: ${st.error}` : word
    }
    case 'noKey':
      return t('settings.ai.stDetail.noKey')
    case 'off':
      return t('settings.ai.stDetail.off')
    default:
      return t('settings.ai.stDetail.unchecked')
  }
}

/** «Anthropic · api.anthropic.com» — what an entry is, under its name (the tasks it serves are `tiersLine`, on a line of their own). */
export function entryHint(t: TFunction, e: AIModel, withHost = true): string {
  const provider = e.env ? t('settings.ai.server') : PROVIDER_NAMES[providerOf(e.url)]
  // no-break around the dot: a line never ends on it
  return [provider, withHost && hostOf(e.url)].filter(Boolean).join('\u00A0·\u00A0')
}

/** «Задачи: лёгкие, сложные» for a model that serves some of the weights only; else ''. */
export function tiersLine(t: TFunction, e: AIModel): string {
  const tiers = TIERS.filter((x) => e.tiers.includes(x))
  return tiers.length > 0 && tiers.length < TIERS.length ? t('settings.ai.tiersOnly', { list: tiers.map((x) => t(`settings.ai.tierWord.${x}`)).join(', ') }) : ''
}

/** A number stays with its unit when a line breaks («5 мин назад», «1 без ключа»). */
export const nb = (s: string) => s.replace(/(\d) (?=\p{L})/gu, '$1\u00A0')
