// The one text button of the app. Capsule shaped; an optional leading icon.
//   primary     — white fill, black label: the main action of a screen («Смотреть»)
//   secondary   — matte glass: a supporting action («Подробнее», «Настроить…»)
//   plain       — accent text, no fill until hovered: an action in a list («Добавить язык»)
//   destructive — glass with a red label («Удалить»); the red fill is the Sheet's
//   link        — accent text inside a line of text, with its font («Скрыть»)
// Sizes: small (36), regular (44), large (52) — the control heights of the
// tokens. An icon-only button is an IconButton, never a Button.
import { forwardRef, type AnchorHTMLAttributes, type ButtonHTMLAttributes, type ReactNode } from 'react'
import { Link, type LinkProps } from 'react-router'
import { Icon, type IconName } from '../components/Icon'
import styles from './Button.module.css'

/** The stroke of a glyph inside a control (Button, IconButton). */
export const GLYPH_STROKE = 2

export type ButtonVariant = 'primary' | 'secondary' | 'plain' | 'destructive' | 'link'
export type ButtonSize = 'small' | 'regular' | 'large'

interface Common {
  variant?: ButtonVariant
  size?: ButtonSize
  icon?: IconName
  loading?: boolean
  children?: ReactNode
}

/** The glyph of each size (--icon-* tokens). */
export const BUTTON_ICON: Record<ButtonSize, number> = { small: 16, regular: 18, large: 20 }

function classes(variant: ButtonVariant, size: ButtonSize, extra?: string) {
  return [styles.button, styles[variant], styles[size], variant === 'secondary' || variant === 'destructive' ? 'glass' : '', extra].filter(Boolean).join(' ')
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
  { variant = 'primary', size = 'regular', icon, loading, children, className, type = 'button', disabled, ...rest },
  ref,
) {
  return (
    <button ref={ref} type={type} className={classes(variant, size, className)} disabled={disabled || loading} aria-busy={loading || undefined} {...rest}>
      <Inner icon={icon} loading={loading} size={size}>
        {children}
      </Inner>
    </button>
  )
})

export type ButtonLinkProps = Common & LinkProps

export function ButtonLink({ variant = 'primary', size = 'regular', icon, children, className, ...rest }: ButtonLinkProps) {
  return (
    <Link className={classes(variant, size, className)} {...rest}>
      <Inner icon={icon} size={size}>
        {children}
      </Inner>
    </Link>
  )
}

export type ButtonAnchorProps = Common & AnchorHTMLAttributes<HTMLAnchorElement>

/** A plain `<a>` (a file download the browser handles) styled as the button. */
export function ButtonAnchor({ variant = 'primary', size = 'regular', icon, children, className, ...rest }: ButtonAnchorProps) {
  return (
    <a className={classes(variant, size, className)} {...rest}>
      <Inner icon={icon} size={size}>
        {children}
      </Inner>
    </a>
  )
}
