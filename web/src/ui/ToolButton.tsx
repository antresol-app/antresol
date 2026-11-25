// The toolbar button: a rounded square of a bar over a picture or a page —
// the full player's mode and tool rows, the readers' top bars, a queue row's
// own tools. A bare glyph (or a glyph and a word: «‹ Книги»), a wash under the
// pointer, white while it is toggled on. Round buttons are IconButtons; this is
// the square one.
//   size   md 40 (default, a bar) · sm 36 (inside a row)
//   toggled  a toggle (aria-pressed): on, it turns white like IconButton's
// A bar paints its tools by setting `--tool-color`, `--tool-hover-fill`,
// `--tool-hover-color`, `--tool-on-fill` and `--tool-on-color` on itself (the
// reader's bars in the page's theme, the full player's in white over the cover);
// without them the tokens draw it on a plain page. `--tool-h` sets the side where a
// bar wants bigger tools on a touch screen (the full player's rows are 44 there).
// `label` is the accessible name and, for a glyph alone, the tooltip (`tooltip`
// overrides it). `icon` is an Icon's name or a glyph of the page's own;
// `children` is the word after the glyph (or the whole content, with no icon).
import { forwardRef, isValidElement, type ButtonHTMLAttributes, type ReactElement, type ReactNode } from 'react'
import { Icon, type IconName } from '../components/Icon'
import styles from './ToolButton.module.css'

export type ToolButtonSize = 'sm' | 'md'

/** The glyph of each size (--icon-xs, --icon-md). */
const GLYPH: Record<ToolButtonSize, number> = { sm: 16, md: 20 }

export interface ToolButtonProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  icon?: IconName | ReactElement
  label: string
  tooltip?: string
  size?: ToolButtonSize
  toggled?: boolean
  children?: ReactNode
}

export const ToolButton = forwardRef<HTMLButtonElement, ToolButtonProps>(function ToolButton(
  { icon, label, tooltip, size = 'md', toggled, children, className, type = 'button', ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={[styles.tool, styles[size], toggled ? styles.on : '', className].filter(Boolean).join(' ')}
      aria-label={label}
      title={tooltip ?? (children == null ? label : undefined)}
      aria-pressed={toggled}
      {...rest}
    >
      {isValidElement(icon) ? icon : icon && <Icon name={icon} size={GLYPH[size]} />}
      {children}
    </button>
  )
})
