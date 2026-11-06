// The one icon-only button: a circle with a glyph. Every round button of the
// app is this component, so one size always means one diameter, one glass,
// one glyph size and one glyph colour (the item page's eye, watchlist, «…»
// and trash are identical but for the trash's red).
//   size     xs 30 (in a list row) · sm 36 · md 44 (toolbars) · lg 52 (item page)
//   variant  glass (default: filled) · plain: no fill until hovered (inside a row)
//   material fill (default, in content) · glass (on a bar) · clear (over artwork, video)
//   tone     destructive: the glyph in red
//   toggled  a toggle (aria-pressed): on, it turns white like «Смотреть»
// `label` is the accessible name and the tooltip (`tooltip` overrides the
// tooltip when the state matters: «В списке» / «Добавить»). `tile` shows
// the label under the glyph where a row of tiles asks for it (ActionBar on
// phones) and hides it elsewhere.
import { forwardRef, isValidElement, useContext, type AnchorHTMLAttributes, type ReactElement, type ButtonHTMLAttributes } from 'react'
import { Link, type LinkProps } from 'react-router'
import { Icon, type IconName } from '../components/Icon'
import { GLYPH_STROKE, MaterialContext, materialClass, type Material } from './Button'
import styles from './IconButton.module.css'

export type IconButtonSize = 'xs' | 'sm' | 'md' | 'lg'

/** The glyph of each diameter (--icon-* tokens). */
export const ICON_GLYPH: Record<IconButtonSize, number> = { xs: 16, sm: 18, md: 20, lg: 24 }

interface Common {
  /** An Icon's name, or a glyph of a page's own set (Files' Finder glyphs) drawn at the size's glyph size. */
  icon: IconName | ReactElement
  /** The accessible name; also the tooltip unless `tooltip` is given. */
  label: string
  tooltip?: string
  size?: IconButtonSize
  variant?: 'glass' | 'plain'
  /** What the filled variant is made of: a fill in content (default), glass on a bar, clear glass over artwork or video. */
  material?: Material
  tone?: 'default' | 'destructive'
  /** A toggle: aria-pressed, white while on. */
  toggled?: boolean
  loading?: boolean
  /** Show `label` (or this shorter text) under the glyph in a row of tiles (ActionBar on phones). */
  tile?: boolean | string
  className?: string
}

function classes({ size = 'md', variant = 'glass', material = 'fill', tone = 'default', toggled, className }: Partial<Common>) {
  const surface = variant === 'glass' ? materialClass(material, styles.fill!) : ''
  return [styles.button, styles[size], styles[variant], surface, surface && material === 'clear' ? styles.clear : '', tone === 'destructive' ? styles.destructive : '', toggled ? styles.on : '', className]
    .filter(Boolean)
    .join(' ')
}

function Inner({ icon, size = 'md', loading, tile, label }: Partial<Common> & { icon: IconName | ReactElement }) {
  return (
    <>
      {loading ? <span className={styles.spinner} aria-hidden /> : isValidElement(icon) ? icon : <Icon name={icon} size={ICON_GLYPH[size]} strokeWidth={GLYPH_STROKE} />}
      {tile && (
        <span className={styles.tileLabel} data-tile-label aria-hidden>
          {typeof tile === 'string' ? tile : label}
        </span>
      )}
    </>
  )
}

export type IconButtonProps = Common & Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'>

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(function IconButton(
  { icon, label, tooltip, size, variant, material: own, tone, toggled, loading, tile, className, type = 'button', disabled, ...rest },
  ref,
) {
  const inherited = useContext(MaterialContext)
  const material = own ?? inherited
  return (
    <button
      ref={ref}
      type={type}
      className={classes({ size, variant, material, tone, toggled, className })}
      aria-label={label}
      title={tooltip ?? label}
      aria-pressed={toggled}
      aria-busy={loading || undefined}
      disabled={disabled || loading}
      {...rest}
    >
      <Inner icon={icon} size={size} loading={loading} tile={tile} label={label} />
    </button>
  )
})

export type IconButtonLinkProps = Common & Omit<LinkProps, 'children'>

/** The round button as a router link. */
export function IconButtonLink({ icon, label, tooltip, size, variant, material: own, tone, toggled, tile, className, ...rest }: IconButtonLinkProps) {
  const inherited = useContext(MaterialContext)
  const material = own ?? inherited
  return (
    <Link className={classes({ size, variant, material, tone, toggled, className })} aria-label={label} title={tooltip ?? label} {...rest}>
      <Inner icon={icon} size={size} tile={tile} label={label} />
    </Link>
  )
}

export type IconButtonAnchorProps = Common & Omit<AnchorHTMLAttributes<HTMLAnchorElement>, 'children'>

/** The round button as a plain `<a>` (a file download the browser handles). */
export function IconButtonAnchor({ icon, label, tooltip, size, variant, material: own, tone, toggled, tile, className, ...rest }: IconButtonAnchorProps) {
  const inherited = useContext(MaterialContext)
  const material = own ?? inherited
  return (
    <a className={classes({ size, variant, material, tone, toggled, className })} aria-label={label} title={tooltip ?? label} {...rest}>
      <Inner icon={icon} size={size} tile={tile} label={label} />
    </a>
  )
}
