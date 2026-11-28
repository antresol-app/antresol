// «Настройки сервера → ИИ-помощник»: the language models Antresol's AI tasks
// ask, one ordered list — the first that answers wins, a failure goes on to
// the next. A «ИИ-помощник» switch, then the models as System Settings lists
// accounts (Mail → Accounts): a row each — the name, «провайдер · адрес ·
// задачи» under it, its status before the chevron — that opens the model's
// sheet (the full error, the key, the tasks it serves, «Проверить», «Удалить»);
// «Изменить» in the page's header turns the same rows into edit rows (− and ≡:
// the order is the priority); «Добавить модель…» is the last row; «Проверить
// все» asks every model that is on. A model that serves the tasks of some
// weights only (light: naming IPTV categories; medium: «Опишите канал»;
// powerful: the hardest) is asked for those only. The key is written, never
// shown again. Without a model the channel is read from the words alone.
// The server's own model (VAULT_LLM_*) is a provider like the others («LiteLLM ·
// с сервера») and its model is chosen in its sheet. «Функции ИИ» lists what the
// server does with a model, a switch each (AIFeatures.tsx).
import { useMemo, useState } from 'react'
import { errorText, testAIModels } from '../../api/client'
import type { AISettings as AI, AIModel, AIStatus } from '../../api/types'
import { useT } from '../../i18n'
import { ago } from '../../state/format'
import { GroupedSection, ListLinkRow, ListRow, ListSelectRow } from '../../ui/List'
import { Sheet } from '../../ui/Sheet'
import { SortableList, SortableRowText } from '../../ui/SortableList'
import { Toggle } from '../../ui/Toggle'
import { AIFeaturesSection } from './AIFeatures'
import { AIModelSheet } from './AIModelSheet'
import { entryHint, nb, providerOf, statusView, tiersLine } from './aiModels'

export { providerOf, AI_PROVIDERS } from './aiModels'

/** The sheet open: an entry's id, or a new one. */
type Open = { id: string } | 'new' | null

/** What the header's «Изменить» needs to know. */
export const aiEditable = (ai?: AI) => (ai?.list.length ?? 0) > 0

export function AISettingsSection({
  ai,
  edit,
  onChange,
  onSave,
}: {
  ai?: AI
  /** «Изменить» is on: the rows are edit rows. */
  edit: boolean
  /** A change that applies at once (a switch, the order); saved in the background. */
  onChange: (ai: AI) => void
  /** A change to wait for (the sheet's): the server's answer; throws when it is refused. */
  onSave: (ai: AI) => Promise<AI>
}) {
  const t = useT()
  const list = useMemo(() => ai?.list ?? [], [ai])
  const [open, setOpen] = useState<Open>(null)
  const [removing, setRemoving] = useState<AIModel | null>(null)
  const [checking, setChecking] = useState(false)
  const [problem, setProblem] = useState('')
  // what the checks of this page found, until the server's own say newer
  const [seen, setSeen] = useState<Record<string, AIStatus>>({})
  const statusOf = (e: AIModel): AIStatus | undefined => {
    const found = seen[e.id]
    const stored = e.status
    // off and «Нет ключа» are the server's to say; otherwise the newer of the two
    if (!found || stored?.state === 'off' || stored?.state === 'noKey') return stored
    return !stored?.at || Date.parse(found.at ?? '') >= Date.parse(stored.at) ? found : stored
  }
  const remember = (rs: { id: string; status: AIStatus }[]) => setSeen((m) => ({ ...m, ...Object.fromEntries(rs.map((r) => [r.id, r.status])) }))

  const current = ai ?? { list: [] }
  const entry = open && open !== 'new' ? list.find((e) => e.id === open.id) : undefined

  /** Saves the list with `draft` in it (a new one when its id is not in the list); the entry as the server keeps it. */
  const saveEntry = async (draft: AIModel): Promise<AIModel | undefined> => {
    const at = list.findIndex((e) => e.id === draft.id)
    const next = at < 0 ? [...list, draft] : list.map((e, i) => (i === at ? draft : e))
    setSeen((m) => Object.fromEntries(Object.entries(m).filter(([id]) => id !== draft.id))) // an edited entry starts afresh
    const out = await onSave({ ...current, list: next })
    return out.list[at < 0 ? next.length - 1 : at]
  }
  /** Asks one saved entry; its status (the sheet shows it, the list too). */
  const check = async (id: string): Promise<AIStatus | undefined> => {
    const r = (await testAIModels({ ids: [id] })).results[0]
    if (r) remember([r])
    return r?.status
  }
  const checkAll = async () => {
    setChecking(true)
    setProblem('')
    try {
      remember((await testAIModels({})).results)
    } catch (e) {
      setProblem(errorText(e, t('settings.ai.checkFailed')))
    } finally {
      setChecking(false)
    }
  }
  const remove = (e: AIModel) => {
    setRemoving(null)
    setOpen(null)
    onChange({ ...current, list: list.filter((x) => x.id !== e.id) })
  }

  // «Проверено 5 мин назад: 2 работают, 1 с ошибкой» — over the models that are on
  const summary = (() => {
    const on = list.map(statusOf).filter((s) => s && s.state !== 'off')
    const at = Math.max(0, ...on.map((s) => (s?.at ? Date.parse(s.at) : 0)))
    if (!at) return t('settings.ai.checkAllHint')
    const n = (state: string) => on.filter((s) => s?.state === state).length
    const parts = [
      n('ok') && t('settings.ai.sum.ok', { count: n('ok') }),
      n('error') && t('settings.ai.sum.error', { count: n('error') }),
      n('noKey') && t('settings.ai.sum.noKey', { count: n('noKey') }),
      n('unchecked') && t('settings.ai.sum.unchecked', { count: n('unchecked') }),
    ].filter(Boolean)
    return nb(t('settings.ai.checkedAgo', { ago: ago(new Date(at).toISOString()), parts: parts.join(', ') }))
  })()

  return (
    <>
      <GroupedSection footer={t('settings.ai.footer')}>
        <ListRow label={t('settings.ai.assistant')}>
          <Toggle label={t('settings.ai.assistant')} checked={!current.off} onChange={(on) => onChange({ ...current, off: !on })} />
        </ListRow>
      </GroupedSection>

      <GroupedSection title={t('settings.ai.models')} footer={edit ? t('settings.ai.priorityEdit') : t('settings.ai.priority')}>
        {edit ? (
          <SortableList
            flush
            label={t('settings.ai.models')}
            items={list}
            getKey={(e) => e.id}
            itemLabel={(e) => e.model}
            removable
            canRemove={(e) => !e.env}
            renderItem={(e) => <SortableRowText title={e.model} detail={entryHint(t, e, false)} />}
            onChange={(next) => {
              // a removal asks first (the key goes with it); the order applies at once
              const gone = list.find((e) => !next.some((x) => x.id === e.id))
              if (gone) setRemoving(gone)
              else onChange({ ...current, list: next })
            }}
          />
        ) : (
          list.map((e) => {
            const sv = statusView(t, statusOf(e))
            const tasks = tiersLine(t, e)
            const hint = (
              <>
                {entryHint(t, e)}
                {tasks && (
                  <>
                    {' '}
                    <br />
                    {tasks}
                  </>
                )}
              </>
            )
            return <ListLinkRow key={e.id} label={e.model} hint={hint} value={sv.text} valueTone={sv.tone} onOpen={() => setOpen({ id: e.id })} />
          })
        )}
        <ListSelectRow tone="accent" label={t('settings.ai.add')} onSelect={() => setOpen('new')} />
      </GroupedSection>

      {list.length > 0 && (
        <GroupedSection>
          <ListSelectRow
            tone="accent"
            label={checking ? t('settings.ai.checking') : t('settings.ai.checkAll')}
            hint={problem ? <span role="alert">{problem}</span> : <span role="status">{summary}</span>}
            disabled={checking}
            onSelect={() => void checkAll()}
          />
        </GroupedSection>
      )}

      <AIFeaturesSection ai={current} onChange={onChange} />

      {open && (open === 'new' || entry) && (
        <AIModelSheet
          key={open === 'new' ? 'new' : open.id}
          entry={entry}
          status={entry ? statusOf(entry) : undefined}
          defaultUrl={list.find((e) => !e.env && providerOf(e.url) === 'litellm')?.url}
          serverUrl={list.find((e) => e.env)?.url}
          onSave={saveEntry}
          onCheck={check}
          onDelete={() => entry && setRemoving(entry)}
          onClose={() => setOpen(null)}
        />
      )}
      {removing && (
        <Sheet
          title={t('settings.ai.deleteTitle', { name: removing.model })}
          message={t('settings.ai.deleteMessage')}
          onCancel={() => setRemoving(null)}
          actions={[
            { label: t('common.cancel'), kind: 'cancel', run: () => setRemoving(null) },
            { label: t('settings.ai.delete'), kind: 'destructive', run: () => remove(removing) },
          ]}
        />
      )}
    </>
  )
}
