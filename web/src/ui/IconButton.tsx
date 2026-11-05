// The one icon-only button: a circle with a glyph. Every round button of the
// app is this component, so one size always means one diameter, one glass,
// one glyph size and one glyph colour (the item page's eye, watchlist, «…»
// and trash are identical but for the trash's red).
//   size     xs 30 (in a list row) · sm 36 · md 44 (toolbars) · lg 52 (item page)
//   variant  glass (default) · plain: no fill until hovered (inside a row)
//   tone     destructive: the glyph in red
//   toggled  a toggle (aria-pressed): on, it turns white like «Смотреть»
// `label` is the accessible name and the tooltip (`tooltip` overrides the
// tooltip when the state matters: «В списке» / «Добавить»). `tile` shows
// the label under the glyph where a row of tiles asks for it (ActionBar on
// phones) and hides it elsewhere.
import { forwardRef, type AnchorHTMLAttributes, type ButtonHTMLAttributes } from 'react'
import { Link, type LinkProps } from 'react-router'
import { Icon, type IconName } from '../components/Icon'
import { GLYPH_STROKE } from './Button'
import styles from './IconButton.module.css'

export type IconButtonSize = 'xs' | 'sm' | 'md' | 'lg'

/** The glyph of each diameter (--icon-* tokens). */
export const ICON_GLYPH: Record<IconButtonSize, number> = { xs: 16, sm: 18, md: 20, lg: 24 }

interface Common {
  icon: IconName
  /** The accessible name; also the tooltip unless `tooltip` is given. */
  label: string
  tooltip?: string
  size?: IconButtonSize
  variant?: 'glass' | 'plain'
  tone?: 'default' | 'destructive'
  /** A toggle: aria-pressed, white while on. */
  toggled?: boolean
  loading?: boolean
  /** Show `label` (or this shorter text) under the glyph in a row of tiles (ActionBar on phones). */
  tile?: boolean | string
  className?: string
}

function classes({ size = 'md', variant = 'glass', tone = 'default', toggled, className }: Partial<Common>) {
  return [styles.button, styles[size], styles[variant], tone === 'destructive' ? styles.destructive : '', toggled ? styles.on : '', variant === 'glass' ? 'glass' : '', className]
    .filter(Boolean)
    .join(' ')
}

function Inner({ icon, size = 'md', loading, tile, label }: Partial<Common> & { icon: IconName }) {
  return (
    <>
      {loading ? <span className={styles.spinner} aria-hidden /> : <Icon name={icon} size={ICON_GLYPH[size]} strokeWidth={GLYPH_STROKE} />}
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
  { icon, label, tooltip, size, variant, tone, toggled, loading, tile, className, type = 'button', disabled, ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={classes({ size, variant, tone, toggled, className })}
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
export function IconButtonLink({ icon, label, tooltip, size, variant, tone, toggled, tile, className, ...rest }: IconButtonLinkProps) {
  return (
    <Link className={classes({ size, variant, tone, toggled, className })} aria-label={label} title={tooltip ?? label} {...rest}>
      <Inner icon={icon} size={size} tile={tile} label={label} />
    </Link>
  )
}

export type IconButtonAnchorProps = Common & Omit<AnchorHTMLAttributes<HTMLAnchorElement>, 'children'>

/** The round button as a plain `<a>` (a file download the browser handles). */
export function IconButtonAnchor({ icon, label, tooltip, size, variant, tone, toggled, tile, className, ...rest }: IconButtonAnchorProps) {
  return (
    <a className={classes({ size, variant, tone, toggled, className })} aria-label={label} title={tooltip ?? label} {...rest}>
      <Inner icon={icon} size={size} tile={tile} label={label} />
    </a>
  )
}
