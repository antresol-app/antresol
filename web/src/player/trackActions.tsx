// The addons' rows of the player's track menu (`playerTrackActions`): for
// a library movie, or the series of an episode — never during online
// playback (the player passes no item then) and never for a kid
// (useAddonSlot). A row whose visible() throws is left out; an element a
// run returns is drawn over the player until it closes (one that throws on
// drawing goes and the player stays).
import { useMemo, type ReactNode } from 'react'
import { addonString, useRegistryVersion, type TrackActionItem } from '../addons/registry'
import { useAddonSheet, useAddonSlot } from '../addons/slots'
import { useLang } from '../i18n'
import type { TrackMenuAction } from './TrackMenu'

export function usePlayerTrackActions(item: TrackActionItem | undefined): { rows: TrackMenuAction[]; node: ReactNode } {
  const lang = useLang()
  const v = useRegistryVersion()
  const slot = useAddonSlot('playerTrackActions')
  const sheet = useAddonSheet()
  const run = sheet.run
  const rows = useMemo(() => {
    void v
    if (!item) return []
    const out: TrackMenuAction[] = []
    slot.forEach((a, i) => {
      try {
        if (a.visible && !a.visible(item)) return
      } catch (e) {
        console.warn(`antresol: addon ${a.addonId} track action failed`, e)
        return
      }
      out.push({
        key: `${a.addonId}:${a.label}:${i}`,
        label: addonString(a.addonId, lang, a.label),
        icon: a.icon,
        run: () => {
          try {
            run(a.addonId, (close) => a.run(item, close))
          } catch (e) {
            console.warn(`antresol: addon ${a.addonId} track action failed`, e)
          }
        },
      })
    })
    return out
  }, [slot, item, lang, v, run])
  return { rows, node: item ? sheet.node : null }
}
