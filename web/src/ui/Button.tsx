// The one text button of the app. Capsule shaped; an optional leading icon.
//   primary     — white fill, black label: the main action of a screen («Смотреть»)
//   secondary   — a grey fill: a supporting action («Настроить…»); over artwork
//                 (the hero's «Подробнее») material="clear", the Liquid Glass of media
//   plain       — accent text, no fill until hovered: an action in a list («Добавить язык»)
//   destructive — the secondary look with a red label («Удалить»); the red fill is the Sheet's
//   link        — accent text inside a line of text, with its font («Скрыть»)
// Sizes: small (36), regular (44), large (52) — the control heights of the
// tokens. An icon-only button is an IconButton, never a Button.
import { createContext, forwardRef, useContext, type AnchorHTMLAttributes, type ButtonHTMLAttributes, type ReactNode } from 'react'
import { Link, type LinkProps } from 'react-router'
import { Icon, type IconName } from '../components/Icon'
import styles from './Button.module.css'

/** The stroke of a glyph inside a control (Button, IconButton). */
export const GLYPH_STROKE = 2

export type ButtonVariant = 'primary' | 'secondary' | 'plain' | 'destructive' | 'link'
export type ButtonSize = 'small' | 'regular' | 'large'
/** What a secondary or destructive button is made of: a fill in content, glass on a bar, clear glass over artwork. */
export type Material = 'fill' | 'glass' | 'clear'

interface Common {
  variant?: ButtonVariant
  size?: ButtonSize
  icon?: IconName
  loading?: boolean
  material?: Material
  children?: ReactNode
}

/** The glyph of each size (--icon-* tokens). */
export const BUTTON_ICON: Record<ButtonSize, number> = { small: 16, regular: 18, large: 20 }

/**
 * The material of the buttons inside (a `material` prop still wins): a row
 * that floats over artwork (ActionTiles on a title page) gives its round
 * buttons clear glass without each one saying so.
 */
export const MaterialContext = createContext<Material>('fill')

/** The class of a material: the ui fill, or one of the glass variants (design/glass.css). */
export const materialClass = (m: Material, fill: string) => (m === 'fill' ? fill : m === 'glass' ? 'glass' : 'glass-clear')

function classes(variant: ButtonVariant, size: ButtonSize, material: Material, extra?: string) {
  const surface = variant === 'secondary' || variant === 'destructive' ? materialClass(material, styles.fill!) : ''
  return [styles.button, styles[variant], styles[size], surface, material === 'clear' && surface ? styles.clear : '', extra].filter(Boolean).join(' ')
}

function Inner({ icon, loading, children, size = 'regular' }: Common) {
  return (
    <>
      {loading ? <span className={styles.spinner} aria-hidden /> : icon ? <Icon name={icon} size={BUTTON_ICON[size]} strokeWidth={GLYPH_STROKE} /> : null}
      {children != null && <span className={styles.label}>{children}</span>}
    </>
  )
}

export type ButtonProps = Common & ButtonHTMLAttributes<HTMLButtonElement>

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'primary', size = 'regular', material: own, icon, loading, children, className, type = 'button', disabled, ...rest },
  ref,
) {
  const inherited = useContext(MaterialContext)
  const material = own ?? inherited
  return (
    <button
      ref={ref}
      type={type}
      className={classes(variant, size, material, className)}
      disabled={disabled || loading}
      aria-busy={loading || undefined}
      {...rest}
    >
      <Inner icon={icon} loading={loading} size={size}>
        {children}
      </Inner>
    </button>
  )
})

export type ButtonLinkProps = Common & LinkProps

export function ButtonLink({ variant = 'primary', size = 'regular', material: own, icon, children, className, ...rest }: ButtonLinkProps) {
  const inherited = useContext(MaterialContext)
  const material = own ?? inherited
  return (
    <Link className={classes(variant, size, material, className)} {...rest}>
      <Inner icon={icon} size={size}>
        {children}
      </Inner>
    </Link>
  )
}

export type ButtonAnchorProps = Common & AnchorHTMLAttributes<HTMLAnchorElement>

/** A plain `<a>` (a file download the browser handles) styled as the button. */
export function ButtonAnchor({ variant = 'primary', size = 'regular', material: own, icon, children, className, ...rest }: ButtonAnchorProps) {
  const inherited = useContext(MaterialContext)
  const material = own ?? inherited
  return (
    <a className={classes(variant, size, material, className)} {...rest}>
      <Inner icon={icon} size={size}>
        {children}
      </Inner>
    </a>
  )
}
