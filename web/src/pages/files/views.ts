// Props shared by the three views (Значки, Список, Колонки).
import type { HTMLAttributes, MouseEvent, ReactNode, Ref } from 'react'
import type { FileEntry } from '../../api/types'
import type { Selection } from './model'

export type ViewMode = 'icons' | 'list' | 'columns'

export interface ItemHandlers {
  /** Click selection (⌘ / Shift aware). */
  onSelect: (e: MouseEvent, entry: FileEntry) => void
  /** Double-click: open the folder, the item page or Quick Look. */
  onOpen: (entry: FileEntry) => void
  /** Right-click on an item (null: on the empty background). */
  onMenu: (e: MouseEvent, entry: FileEntry | null) => void
  /** Click on the empty background. */
  onClear: () => void
}

/** The focusable container of a view: keyboard, label, active descendant. */
export type ContainerProps = HTMLAttributes<HTMLDivElement> & { ref?: Ref<HTMLDivElement> }

export interface ViewProps extends ItemHandlers {
  entries: FileEntry[]
  selection: Selection
  container: ContainerProps
  /** The view has keyboard focus (selection is blue, otherwise grey). */
  active: boolean
  /** Shown instead of items when the folder is empty. */
  empty?: ReactNode
}

/** Stable DOM id of an entry, for aria-activedescendant. */
export function itemId(path: string): string {
  let h = 2166136261
  for (let i = 0; i < path.length; i++) {
    h ^= path.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return `fi-${(h >>> 0).toString(36)}`
}
