// One model of «ИИ-помощник»'s list, as a sheet: a new one («Добавить
// модель…»: the provider, then its address, the key and the model — the
// pop-up of what the endpoint lists, «Другая…» for a name it does not) or an
// existing one (its status in full, the key, the tasks it serves, «Проверить»,
// «Удалить»). A new one starts with its kind, a segmented control as iOS puts at
// the top of an «Add…» sheet: a language model, or speech recognition (the
// audiobooks' transcripts: Groq, OpenAI, Gemini or a server of one's own, no
// tasks to choose, its list is the speech models only). The server's own model (VAULT_LLM_*) is a provider like the
// others: its address and key stay the environment's (read-only), its model is
// chosen from what the endpoint lists. A LiteLLM added while the server has one
// starts from its address and asks with the server's key, which this page never
// sees (no key field, a note).
// The key field keeps today's behaviour: Enter saves the model and checks it
// at once (the sheet stays, the answer is in its status), a provider that asks
// for a key is not saved without one, and a stored key shows «Сохранён».
import { useEffect, useRef, useState } from 'react'
import { errorText, listAIModels, testAIModels } from '../../api/client'
import type { AIModel, AIStatus, AITier } from '../../api/types'
import { useT } from '../../i18n'
import { ago } from '../../state/format'
import { Badge } from '../../ui/Badge'
import { GroupedSection, ListRow, ListSelectRow } from '../../ui/List'
import { Notice } from '../../ui/Notice'
import { Select } from '../../ui/Select'
import { SegmentedControl } from '../../ui/SegmentedControl'
import { Sheet } from '../../ui/Sheet'
import { TextField } from '../../ui/TextField'
import { Toggle } from '../../ui/Toggle'
import { AI_PROVIDERS, LITELLM_URL, PROVIDER_NAMES, SPEECH_PROVIDERS, SPEECH_PROVIDER_LIST, TIERS, hostOf, kindOf, nb, needsKey, providerOf, statusDetail, statusView, validUrl, type Kind, type Provider } from './aiModels'
import styles from './Settings.module.css'

const OTHER = '\u0000other'

interface Draft {
  kind: Kind
  provider: Provider
  url: string
  model: string
  /** Typed this time (a stored key is not here). */
  key: string
  tiers: AITier[]
  off: boolean
  /** Asks with the server's own key (holds only while the address is the server's host). */
  serverKey: boolean
}

/** A new entry of `kind` and `provider`; a LiteLLM starts from the server's own address (and key) when it has one, else the last one used (a text model's only). */
const startOf = (kind: Kind, provider: Provider, defaultUrl?: string, serverUrl?: string): Draft => {
  if (kind === 'speech') {
    const own = provider === 'groq' || provider === 'openai' || provider === 'gemini' ? SPEECH_PROVIDERS[provider] : undefined
    return { kind, provider, url: own?.url ?? LITELLM_URL, model: own?.model ?? '', key: '', tiers: [...TIERS], off: false, serverKey: false }
  }
  return {
    kind,
    provider,
    url: provider === 'litellm' ? (serverUrl ?? defaultUrl ?? LITELLM_URL) : AI_PROVIDERS[provider].url,
    model: provider === 'litellm' ? '' : AI_PROVIDERS[provider].models.medium,
    key: '',
    tiers: [...TIERS],
    off: false,
    serverKey: provider === 'litellm' && !!serverUrl,
  }
}

const fromEntry = (e: AIModel): Draft => ({ kind: kindOf(e), provider: providerOf(e.url), url: e.url, model: e.model, key: '', tiers: [...e.tiers], off: !!e.off, serverKey: !!e.keyFromEnv })

const sameTiers = (a: AITier[], b: AITier[]) => TIERS.every((x) => a.includes(x) === b.includes(x))

export function AIModelSheet({
  entry,
  status,
  defaultUrl,
  serverUrl,
  onSave,
  onCheck,
  onDelete,
  onClose,
}: {
  /** The model; none: a new one. */
  entry?: AIModel
  /** Its status as the page has it (a check of the page's or the server's). */
  status?: AIStatus
  /** The address a new LiteLLM starts from when the server has no LiteLLM of its own (the last one used). */
  defaultUrl?: string
  /** The server's own address (VAULT_LLM_URL), when it has one: a LiteLLM there asks with the server's key. */
  serverUrl?: string
  /** Saves it (the server's list, with it); the entry as the server keeps it. Throws when refused. */
  onSave: (e: AIModel) => Promise<AIModel | undefined>
  /** Asks the saved model; its status. */
  onCheck: (id: string) => Promise<AIStatus | undefined>
  onDelete: () => void
  onClose: () => void
}) {
  const t = useT()
  // a new LiteLLM may take the server's own address and key only if that is one
  const serverLiteLLM = serverUrl && providerOf(serverUrl) === 'litellm' ? serverUrl : undefined
  // the model as the server keeps it: the entry opened, or the one this sheet saved
  const [saved, setSaved] = useState<AIModel | undefined>(entry)
  const [draft, setDraft] = useState<Draft>(entry ? fromEntry(entry) : startOf('text', 'litellm', defaultUrl, serverLiteLLM))
  const speech = draft.kind === 'speech'
  const [busy, setBusy] = useState<'save' | 'check' | null>(null)
  const [error, setError] = useState('')
  // what a check of this sheet found, until the model is saved again
  const [found, setFound] = useState<AIStatus | undefined>()
  const env = !!saved?.env
  const field = (patch: Partial<Draft>) => setDraft((d) => ({ ...d, ...patch }))

  const sameHost = hostOf(draft.url) !== '' && hostOf(draft.url) === hostOf(saved?.url ?? '')
  // the server's own key: asked with while the address is the server's host, and there is no key field then
  const viaServer = !env && draft.serverKey && !!serverUrl && hostOf(draft.url) !== '' && hostOf(draft.url) === hostOf(serverUrl)
  const keyStored = !env && !!saved?.keySet && sameHost && !saved.keyFromEnv
  const keyReady = env || viaServer || !needsKey(draft.url, draft.kind) || draft.key !== '' || keyStored
  const complete = validUrl(draft.url) && draft.model.trim() !== '' && keyReady
  const dirty =
    !saved ||
    draft.off !== !!saved.off ||
    !sameTiers(draft.tiers, saved.tiers) ||
    draft.model.trim() !== saved.model ||
    (!env && (draft.url.trim().replace(/\/+$/, '') !== saved.url || draft.key !== '' || viaServer !== !!saved.keyFromEnv))

  // ---- the models the endpoint lists (nothing billed) ----
  const [names, setNames] = useState<string[] | null>(null)
  // the endpoint was asked and listed nothing
  const [tried, setTried] = useState(false)
  const [custom, setCustom] = useState(false)
  // the key counts for discovery once it is committed (blur, Enter), not at every letter
  const [keyStamp, setKeyStamp] = useState(0)
  const typed = useRef(draft.key)
  typed.current = draft.key
  // the key the models were last asked with (a pause in typing asks again only for a new one)
  const asked = useRef('')
  /** Asks the endpoint again if the key is not the one it was asked with. */
  const askAgain = () => {
    if (typed.current !== asked.current) setKeyStamp((n) => n + 1)
  }
  useEffect(() => {
    asked.current = typed.current
    setNames(null)
    setTried(false)
    if (!validUrl(draft.url) || !keyReady) return
    const ac = new AbortController()
    const timer = setTimeout(() => {
      listAIModels({ url: draft.url, key: typed.current || undefined, id: saved?.id, serverKey: viaServer || undefined, kind: speech ? 'speech' : undefined }, ac.signal).then(
        (r) => {
          if (ac.signal.aborted) return
          setNames(r.models.length > 0 ? r.models.map((m) => m.id) : null)
          setTried(true)
        },
        () => undefined,
      )
    }, 400)
    return () => {
      clearTimeout(timer)
      ac.abort()
    }
    // keyReady changes with the key's first letter: the stamp, not the letters, asks again
  }, [draft.url, keyStamp, env, saved?.id, keyStored, viaServer, speech])
  // a key typed or pasted asks once the typing pauses (blur and Enter ask at once)
  useEffect(() => {
    if (!draft.key) return
    const timer = setTimeout(askAgain, 800)
    return () => clearTimeout(timer)
  }, [draft.key])

  const choose = (p: Provider, kind: Kind = draft.kind) => {
    setDraft(startOf(kind, p, defaultUrl, serverLiteLLM))
    setCustom(false)
    setFound(undefined)
    setError('')
  }
  const chooseKind = (kind: Kind) => choose(kind === 'speech' ? 'groq' : 'litellm', kind)
  const toggleTier = (x: AITier, on: boolean) => field({ tiers: TIERS.filter((y) => (y === x ? on : draft.tiers.includes(y))) })

  const toEntry = (): AIModel => ({
    id: saved?.id ?? '',
    kind: speech ? 'speech' : undefined,
    env: saved?.env,
    url: draft.url.trim(),
    model: draft.model.trim(),
    key: draft.key || undefined,
    keyFromEnv: viaServer || undefined,
    tiers: draft.tiers,
    off: draft.off || undefined,
  })

  /** Saves the model if it changed; the entry as kept. */
  const commit = async (): Promise<AIModel | undefined> => {
    if (!dirty && saved) return saved
    const out = await onSave(toEntry())
    if (!out) throw new Error(t('settings.saveFailed'))
    setSaved(out)
    setDraft(fromEntry(out))
    setFound(undefined)
    return out
  }

  /** Enter in the key field, and «Сохранить»: saves, checks. `stay`: the sheet stays with the answer in it. */
  const saveAndCheck = async (stay: boolean) => {
    if (!complete || busy) return
    setBusy('save')
    setError('')
    try {
      const out = await commit()
      if (!out) return
      if (stay) {
        setBusy('check')
        setFound(await onCheck(out.id))
      } else {
        onClose()
        void onCheck(out.id).catch(() => undefined)
      }
    } catch (e) {
      setError(errorText(e, t('settings.saveFailed')))
    } finally {
      setBusy(null)
    }
  }

  /** «Проверить»: the saved model as it is stored; a changed or new one is asked as typed, nothing saved. */
  const run = async () => {
    if (busy) return
    if (!keyReady) {
      setError(t('settings.ai.needKey'))
      return
    }
    setBusy('check')
    setError('')
    try {
      if (saved && !dirty) setFound(await onCheck(saved.id))
      else setFound((await testAIModels({ entry: toEntry() })).results[0]?.status)
    } catch (e) {
      setError(errorText(e, t('settings.ai.checkFailed')))
    } finally {
      setBusy(null)
    }
  }

  const shown = found ?? (saved ? status : undefined)
  const sv = statusView(t, draft.off && saved ? { state: 'off' } : shown)
  const badge = { success: 'success', warning: 'warning', destructive: 'destructive', neutral: 'default' } as const
  const when = shown?.at && (shown.state === 'ok' || shown.state === 'error') ? t(shown.source === 'task' ? 'settings.ai.src.task' : 'settings.ai.src.check', { ago: ago(shown.at) }) : ''
  const detail = nb([statusDetail(t, draft.off && saved ? { state: 'off' } : shown), when].filter(Boolean).join(' · '))
  const title = saved ? saved.model : t('settings.ai.sheet.add')
  const options = names && (!draft.model || names.includes(draft.model) ? names : [draft.model, ...names])

  // the model: typed (the endpoint lists nothing, or «Другая…») or chosen from what it lists
  const modelField = (
    <TextField
      label={t('settings.ai.modelName')}
      hint={saved || !tried || !validUrl(draft.url) || !keyReady ? undefined : t('settings.ai.modelNone')}
      autoComplete="off"
      spellCheck={false}
      value={draft.model}
      onChange={(e) => field({ model: e.target.value })}
    />
  )
  const modelSelect = options && (
    <ListRow label={t('settings.ai.model')}>
      <Select
        label={t('settings.ai.model')}
        placement="bottom-end"
        value={draft.model}
        options={[...options.map((m) => ({ value: m, label: m, lang: 'zxx' })), { value: OTHER, label: t('settings.ai.modelOther') }]}
        onChange={(m) => (m === OTHER ? setCustom(true) : field({ model: m }))}
      />
    </ListRow>
  )
  const modelRow =
    names && !custom && options ? (
      modelSelect
    ) : (
      <ListRow label={t('settings.ai.modelName')} wide>
        {modelField}
      </ListRow>
    )

  return (
    <Sheet
      title={title}
      role="dialog"
      size="large"
      closeButton={false}
      busy={busy === 'save'}
      onCancel={onClose}
      actions={[
        { label: t('common.cancel'), kind: 'cancel', run: onClose },
        { label: saved && !dirty ? t('common.done') : t('common.save'), kind: 'primary', disabled: !complete || busy === 'check', run: () => (saved && !dirty ? onClose() : void saveAndCheck(false)) },
      ]}
    >
      <>
        {error && <Notice tone="destructive">{error}</Notice>}

        {saved && (
          <GroupedSection footer={<span role="status">{detail}</span>}>
            <ListRow label={t('settings.ai.use')}>
              <Toggle label={t('settings.ai.use')} checked={!draft.off} onChange={(on) => field({ off: !on })} />
            </ListRow>
            <ListRow label={t('settings.ai.status')}>
              <Badge tone={badge[sv.tone]} round size="status">
                {sv.text}
              </Badge>
            </ListRow>
            <ListSelectRow tone="accent" label={busy === 'check' ? t('settings.ai.checking') : t('settings.ai.check')} disabled={busy !== null} onSelect={() => void run()} />
          </GroupedSection>
        )}

        {!saved && (
          <GroupedSection footer={t(speech ? 'settings.ai.kind.speech.hint' : 'settings.ai.kind.text.hint')}>
            <ListRow label={t('settings.ai.kind')} stack>
              <SegmentedControl
                label={t('settings.ai.kind')}
                value={draft.kind}
                onChange={chooseKind}
                fill
                segments={[
                  { value: 'text', label: t('settings.ai.kind.text') },
                  { value: 'speech', label: t('settings.ai.kind.speech') },
                ]}
              />
            </ListRow>
          </GroupedSection>
        )}

        {env ? (
          <GroupedSection title={saved && speech ? t('settings.ai.kind.speech') : undefined} footer={t(speech ? 'settings.ai.envNoteSpeech' : 'settings.ai.envNote', { model: saved?.modelDefault ?? '' })}>
            <ListRow label={t('settings.ai.provider')}>{PROVIDER_NAMES[providerOf(saved?.url ?? '')]}</ListRow>
            <ListRow label={t('settings.ai.url')}>{saved?.url}</ListRow>
            {modelRow}
          </GroupedSection>
        ) : (
          <GroupedSection title={saved && speech ? t('settings.ai.kind.speech') : undefined} footer={viaServer ? t('settings.ai.serverKeyNote') : undefined}>
            {!saved && (
              <ListRow label={t('settings.ai.provider')}>
                <Select
                  label={t('settings.ai.provider')}
                  placement="bottom-end"
                  value={draft.provider}
                  options={(speech ? SPEECH_PROVIDER_LIST : (Object.keys(PROVIDER_NAMES) as Provider[])).map((p) => ({ value: p, label: p === 'litellm' ? t('settings.ai.litellm') : PROVIDER_NAMES[p] }))}
                  onChange={(p) => choose(p)}
                />
              </ListRow>
            )}
            <ListRow label={t('settings.ai.connection')} wide>
              <div className={styles.aiFields}>
                <TextField
                  label={t('settings.ai.url')}
                  autoComplete="off"
                  spellCheck={false}
                  inputMode="url"
                  value={draft.url}
                  onChange={(e) => field({ url: e.target.value })}
                />
                {!names || custom ? modelField : null}
                {draft.provider !== 'ollama' && !viaServer && (
                  <TextField
                    label={t('settings.ai.key')}
                    placeholder={keyStored ? t('settings.ai.keySaved') : undefined}
                    type="password"
                    revealable
                    autoComplete="new-password"
                    enterKeyHint="go"
                    value={draft.key}
                    onChange={(e) => field({ key: e.target.value })}
                    onBlur={askAgain}
                    onKeyDown={(e) => {
                      // Enter: save the model with its key and check it at once
                      if (e.key !== 'Enter' || e.nativeEvent.isComposing) return
                      e.preventDefault()
                      e.stopPropagation()
                      askAgain()
                      void saveAndCheck(true)
                    }}
                  />
                )}
              </div>
            </ListRow>
            {names && !custom && options && modelSelect}
          </GroupedSection>
        )}

        {!speech && (
          <GroupedSection title={t('settings.ai.tasks')} footer={t('settings.ai.tasksFooter')}>
            {TIERS.map((x) => (
              <ListRow key={x} label={t(`settings.ai.tier.${x}`)} hint={t(`settings.ai.tier.${x}.hint`)}>
                <Toggle label={t(`settings.ai.tier.${x}`)} checked={draft.tiers.includes(x)} disabled={draft.tiers.length === 1 && draft.tiers.includes(x)} onChange={(on) => toggleTier(x, on)} />
              </ListRow>
            ))}
          </GroupedSection>
        )}

        {saved && !env && (
          <GroupedSection>
            <ListSelectRow tone="destructive" label={t('settings.ai.delete')} onSelect={onDelete} />
          </GroupedSection>
        )}
      </>
    </Sheet>
  )
}
