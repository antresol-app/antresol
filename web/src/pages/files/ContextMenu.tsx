// Finder's right-click menu: the app's menu (ui/Menu PointMenu) opened at
// the click, with the files' glyphs and their keys («⌘I»). On a phone it is
// the same menu's sheet across the bottom, titled with what it acts on.
import { MenuItem, MenuSeparator, PointMenu } from '../../ui/Menu'
import { Glyph, type GlyphName } from './glyphs'

export interface MenuAction {
  id: string
  label: string
  icon?: GlyphName
  shortcut?: string
  disabled?: boolean
  destructive?: boolean
  run: () => void
}
export type MenuEntry = MenuAction | 'separator'

export interface ContextMenuProps {
  x: number
  y: number
  /** Accessible name and, on a phone, the menu's heading. */
  title: string
  items: MenuEntry[]
  onClose: (refocus?: boolean) => void
}

export function ContextMenu({ x, y, title, items, onClose }: ContextMenuProps) {
  return (
    <PointMenu x={x} y={y} label={title} onClose={onClose}>
      {items.map((it, i) =>
        it === 'separator' ? (
          <MenuSeparator key={`sep${i}`} />
        ) : (
          <MenuItem
            key={it.id}
            label={it.label}
            icon={it.icon && <Glyph name={it.icon} size={16} />}
            shortcut={it.shortcut}
            disabled={it.disabled}
            destructive={it.destructive}
            onSelect={it.run}
          />
        ),
      )}
    </PointMenu>
  )
}
