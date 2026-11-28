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

/** What a new speech entry of each provider starts from (the speech service's own addresses: Gemini's is the native one). */
export const SPEECH_PROVIDERS: Record<'groq' | 'openai' | 'gemini', { url: string; model: string }> = {
  groq: { url: 'https://api.groq.com/openai/v1', model: 'whisper-large-v3-turbo' },
  openai: { url: 'https://api.openai.com/v1', model: 'whisper-1' },
  gemini: { url: 'https://generativelanguage.googleapis.com/v1beta', model: 'gemini-3.5-flash-lite' },
}

/** The providers a speech entry is made for, in the pop-up's order. */
export const SPEECH_PROVIDER_LIST: Provider[] = ['groq', 'openai', 'gemini', 'litellm']

/** What an entry does. */
export type Kind = 'text' | 'speech'

export const kindOf = (e: Pick<AIModel, 'kind'>): Kind => (e.kind === 'speech' ? 'speech' : 'text')

/** The list as the page shows it: the language models, then the speech ones (each kind keeps its own order — the order is the priority only among its own kind). */
export const byKind = <T extends Pick<AIModel, 'kind'>>(list: T[]): T[] => [...list.filter((e) => kindOf(e) === 'text'), ...list.filter((e) => kindOf(e) === 'speech')]

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
  // Gemini's speech address is the native one, not the OpenAI layer's
  if (hostOf(url) === 'generativelanguage.googleapis.com') return 'gemini'
  for (const [p, v] of Object.entries(AI_PROVIDERS)) if (p !== 'ollama' && url.startsWith(v.url)) return p as Provider
  if (/:11434(\/|$)/.test(url)) return 'ollama'
  return 'litellm'
}

/** A provider that asks for a key (the others — a LiteLLM, an own server, an Ollama — may have none). */
export const needsKey = (url: string, kind: Kind = 'text') => {
  const p = providerOf(url)
  return kind === 'speech' ? p === 'groq' || p === 'openai' || p === 'gemini' : p !== 'litellm' && p !== 'ollama'
}

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

/** «Anthropic · api.anthropic.com» — what an entry is, under its name (the tasks it serves are `tiersLine`, on a line of their own).
 * The server's own: «LiteLLM · с сервера» — its provider like any other, and where it comes from in place of the address. */
export function entryHint(t: TFunction, e: AIModel, withHost = true): string {
  const provider = PROVIDER_NAMES[providerOf(e.url)]
  const rest = [provider, e.env ? t('settings.ai.fromServer').replace(/ /g, '\u00A0') : withHost && hostOf(e.url)].filter(Boolean).join('\u00A0·\u00A0')
  // a speech entry says so first: «Распознавание речи · Groq · api.groq.com». The mark stays whole and
  // the line may break after its dot, so a phone's narrow column never splits «Распознавание / речи»
  return kindOf(e) === 'speech' ? `${t('settings.ai.kind.speech.mark').replace(/ /g, '\u00A0')}\u00A0· ${rest}` : rest
}

/** «Задачи: лёгкие, сложные» for a model that serves some of the weights only; else ''. */
export function tiersLine(t: TFunction, e: AIModel): string {
  if (kindOf(e) === 'speech') return '' // speech does not have the weights of tasks
  const tiers = TIERS.filter((x) => e.tiers.includes(x))
  return tiers.length > 0 && tiers.length < TIERS.length ? t('settings.ai.tiersOnly', { list: tiers.map((x) => t(`settings.ai.tierWord.${x}`)).join(', ') }) : ''
}

/** A number stays with its unit when a line breaks («5 мин назад», «1 без ключа»). */
export const nb = (s: string) => s.replace(/(\d) (?=\p{L})/gu, '$1\u00A0')
