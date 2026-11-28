// «ИИ-помощник → Функции ИИ»: everything the server does with a model, one
// switch each. The ids are the server's (channels.Features); a feature that
// is not in `ai.features` is on, so one added later starts on.
import type { AISettings } from '../../api/types'

export const AI_FEATURES = ['describe', 'franchises', 'iptvCategories', 'iptvVariants', 'bookNames', 'chapters', 'transcripts'] as const

export type AIFeature = (typeof AI_FEATURES)[number]

export const featureOn = (ai: AISettings | undefined, id: AIFeature) => ai?.features?.[id] !== false

/** The settings with one feature switched; only the ones that are off are kept. */
export function withFeature(ai: AISettings, id: AIFeature, on: boolean): AISettings {
  const features = { ...ai.features }
  if (on) delete features[id]
  else features[id] = false
  // an empty object, not a missing one: the server keeps what it has when `features` is absent
  return { ...ai, features }
}
