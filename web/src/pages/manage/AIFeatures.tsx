// «ИИ-помощник → Функции ИИ»: every thing the server does with a model, a row
// with a switch (System Settings: the label, a line under it, the switch on
// the trailing edge). All on until switched off. While «ИИ-помощник» itself is
// off the list stays, dimmed and disabled: nothing asks a model then.
import type { AISettings as AI } from '../../api/types'
import { useT } from '../../i18n'
import { GroupedSection, ListRow } from '../../ui/List'
import { Toggle } from '../../ui/Toggle'
import { AI_FEATURES, featureOn, withFeature } from './aiFeatureList'

export function AIFeaturesSection({ ai, onChange }: { ai: AI; onChange: (ai: AI) => void }) {
  const t = useT()
  const off = !!ai.off
  return (
    <GroupedSection title={t('settings.ai.features')} footer={off ? t('settings.ai.featuresOff') : t('settings.ai.featuresFooter')} dimmed={off}>
      {AI_FEATURES.map((id) => (
        <ListRow key={id} label={t(`settings.ai.feat.${id}`)} hint={t(`settings.ai.feat.${id}.hint`)}>
          <Toggle label={t(`settings.ai.feat.${id}`)} checked={featureOn(ai, id)} disabled={off} onChange={(on) => onChange(withFeature(ai, id, on))} />
        </ListRow>
      ))}
    </GroupedSection>
  )
}
