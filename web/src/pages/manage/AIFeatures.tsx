// «ИИ-помощник → Функции ИИ»: every thing the server does with a model, a row
// with a switch (System Settings: the label, a line under it, the switch on
// the trailing edge). All on until switched off. While «ИИ-помощник» itself is
// off the list stays, dimmed and disabled: nothing asks a model then.
// «Расшифровка аудиокниг» needs a speech model of «Модели»: while it is on and
// there is none that can be asked (none, switched off, or without its key), the
// row says so in the warning tone.
import type { AISettings as AI } from '../../api/types'
import { useT } from '../../i18n'
import { GroupedSection, ListRow } from '../../ui/List'
import { Toggle } from '../../ui/Toggle'
import { AI_FEATURES, featureOn, withFeature } from './aiFeatureList'

export function AIFeaturesSection({ ai, speech, onChange }: { ai: AI; /** 'ready': a speech model that can be asked is in the list; 'unusable': there are some, all off or without a key; 'none'. */ speech: 'ready' | 'unusable' | 'none'; onChange: (ai: AI) => void }) {
  const t = useT()
  const off = !!ai.off
  return (
    <GroupedSection title={t('settings.ai.features')} footer={off ? t('settings.ai.featuresOff') : t('settings.ai.featuresFooter')} dimmed={off}>
      {AI_FEATURES.map((id) => (
        <ListRow
          key={id}
          label={t(`settings.ai.feat.${id}`)}
          hint={t(`settings.ai.feat.${id}.hint`)}
          status={id === 'transcripts' && !off && featureOn(ai, id) && speech !== 'ready' ? t(speech === 'none' ? 'settings.ai.feat.transcripts.noSpeech' : 'settings.ai.feat.transcripts.speechUnusable') : undefined}
          statusTone="warning"
        >
          <Toggle label={t(`settings.ai.feat.${id}`)} checked={featureOn(ai, id)} disabled={off} onChange={(on) => onChange(withFeature(ai, id, on))} />
        </ListRow>
      ))}
    </GroupedSection>
  )
}
