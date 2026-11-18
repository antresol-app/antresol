// The addons' panels under the action row of an item's page and of a
// title's page (`itemPanels`; there `item.id` is 0 and `title` the title);
// with `inRow`, the panels that are buttons of the row itself (`inRow: true`,
// drawn by AddonItemActions among the round buttons) instead.
// Each is drawn inside its own boundary: one that throws leaves the page
// and the other panels as they are. None for a kid.
import type { ItemDetail, TitleInfo } from '../api/types'
import { AddonBoundary, useAddonSlot } from '../addons/slots'

export function ItemPanels({ item, title, inRow = false }: { item: ItemDetail; title?: TitleInfo; inRow?: boolean }) {
  const panels = useAddonSlot('itemPanels')
  return (
    <>
      {panels.map((p, i) => {
        if (Boolean(p.inRow) !== inRow) return null
        const Panel = p.element
        return (
          <AddonBoundary key={`${p.addonId}:${i}`} addonId={p.addonId} fallback={null}>
            <Panel item={item} title={title} />
          </AddonBoundary>
        )
      })}
    </>
  )
}
