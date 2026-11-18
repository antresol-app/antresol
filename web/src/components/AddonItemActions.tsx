// The item page's actions that addons add: round buttons after the core
// ones (phone tiles too). Each one is drawn inside its own boundary, so an
// addon whose visible() or button throws drops out and the page says so
// under the row; a run() that fails says so too, and the text a run()
// returns shows there in a notice. An element a run() returns is drawn
// (the addon's sheet) until it calls close(). `watchWay` entries are not
// buttons here: they are ways of the page's «Смотреть ▾». The `inRow`
// panels (a toggle with a state of its own) end the row.
import { useEffect, useState } from 'react'
import type { ItemDetail } from '../api/types'
import { errorText } from '../api/client'
import { useT } from '../i18n'
import { IconButton } from '../ui/IconButton'
import type { Contribution } from '../addons/registry'
import { AddonBoundary, useAddonLabel, useAddonSheet, useAddonSlot } from '../addons/slots'
import { ItemPanels } from './ItemPanels'

type Report = { onProblem: (text: string) => void; onNotice: (text: string) => void }
type Run = ReturnType<typeof useAddonSheet>['run']

function Action({ action, item, onProblem, onNotice, sheet }: { action: Contribution<'itemActions'>; item: ItemDetail; sheet: Run } & Report) {
  const t = useT()
  const label = useAddonLabel(action.addonId)
  const [busy, setBusy] = useState(false)
  if (action.visible && !action.visible(item)) return null
  const run = async () => {
    setBusy(true)
    onProblem('')
    onNotice('')
    try {
      const said = await sheet(action.addonId, (close) => action.run(item, close))
      if (typeof said === 'string') onNotice(said)
    } catch (e) {
      console.warn(`antresol: addon ${action.addonId} action failed`, e)
      onProblem(errorText(e, t('addons.actionFailed')))
    } finally {
      setBusy(false)
    }
  }
  return <IconButton size="lg" icon={action.icon} label={label(action.label)} tile disabled={busy} onClick={() => void run()} />
}

/** The addons' actions for `item`; `onProblem` (a failure) and `onNotice` (what an action said) get the text to show under the row ('' clears it). */
export function AddonItemActions({ item, onProblem, onNotice, onBroken }: { item: ItemDetail; onBroken: () => void } & Report) {
  const t = useT()
  const actions = useAddonSlot('itemActions')
  const sheet = useAddonSheet(() => onProblem(t('addons.actionFailed')))
  const { closeAll } = sheet
  // a sheet is of the item it was opened on
  useEffect(() => closeAll(), [item.id, closeAll])
  return (
    <>
      {actions.map((a, i) =>
        a.watchWay ? null : (
          <AddonBoundary key={`${a.addonId}:${i}`} addonId={a.addonId} fallback={null} onError={onBroken}>
            <Action action={a} item={item} onProblem={onProblem} onNotice={onNotice} sheet={sheet.run} />
          </AddonBoundary>
        ),
      )}
      {/* the addons' toggles of the row («Следить»): panels that are buttons, after the actions */}
      <ItemPanels item={item} inRow />
      {sheet.node}
    </>
  )
}
