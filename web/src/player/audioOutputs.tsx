// «Воспроизвести на…»: the addons' outputs of the music player
// (`audioOutputs`), buttons of «Сейчас играет». Never for a kid
// (useAddonSlot). An element a press returns (the output's picker) is drawn
// inside the player until it closes; one that throws goes and the player
// stays.
import { useMemo, type ReactNode } from 'react'
import { addonString, useRegistryVersion } from '../addons/registry'
import { useAddonSheet, useAddonSlot } from '../addons/slots'
import type { IconName } from '../components/Icon'
import { useLang } from '../i18n'

export interface OutputButton {
  key: string
  label: string
  icon: IconName
  run: () => void
}

export function useAudioOutputs(): { buttons: OutputButton[]; node: ReactNode } {
  const lang = useLang()
  const v = useRegistryVersion()
  const slot = useAddonSlot('audioOutputs')
  const sheet = useAddonSheet()
  const run = sheet.run
  const buttons = useMemo(() => {
    void v
    return slot.map((o, i) => ({
      key: `${o.addonId}:${o.label}:${i}`,
      label: addonString(o.addonId, lang, o.label),
      icon: o.icon ?? 'airplayAudio',
      run: () => {
        try {
          run(o.addonId, (close) => o.run(close))
        } catch (e) {
          console.warn(`antresol: addon ${o.addonId} output failed`, e)
        }
      },
    }))
  }, [slot, lang, v, run])
  return { buttons, node: sheet.node }
}
