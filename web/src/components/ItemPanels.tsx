// The addons' panels under the action row of an item's page and of a
// title's page (`itemPanels`; there `item.id` is 0 and `title` the title).
// Each is drawn inside its own boundary: one that throws leaves the page
// and the other panels as they are. None for a kid.
import type { ItemDetail, TitleInfo } from '../api/types'
import { AddonBoundary, useAddonSlot } from '../addons/slots'

export function ItemPanels({ item, title }: { item: ItemDetail; title?: TitleInfo }) {
  const panels = useAddonSlot('itemPanels')
  return (
    <>
      {panels.map((p, i) => {
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
