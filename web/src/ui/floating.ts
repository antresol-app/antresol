// The one floating-positioning primitive: where a menu (Menu, Select,
// MultiSelect) goes next to its trigger. The menu lives in a portal with
// position: fixed, so no card's overflow can clip it and no card grows
// with it. It opens on the preferred side when it fits there, else on the
// other side when that one fits, else on the roomier side with its height
// cut to scroll. The band it may use runs from the top bar (`data-top-bar`)
// to the tab bar (`data-bottom-bar`), or the window's edges; inside a sheet
// (a dialog covers both bars) only the window counts.

export type Placement = 'bottom-start' | 'bottom-end' | 'top-start' | 'top-end'

export interface Box {
  top: number
  bottom: number
  left: number
  right: number
  width: number
  height: number
}

export interface FloatInput {
  anchor: Box
  /** The floating element's natural size. */
  size: { width: number; height: number }
  viewport: { width: number; height: number }
  /** The usable band: nothing above `ceiling`, nothing below `floor`. */
  ceiling: number
  floor: number
  placement: Placement
  /** Between the trigger and the menu. */
  gap?: number
  /** Kept free at the band's and the window's edges. */
  margin?: number
  /** The tallest a menu ever gets, even with room. */
  cap?: number
  /** The window's safe-area insets at the sides (a phone on its side): kept free too. */
  insets?: { left: number; right: number }
}

export interface FloatResult {
  top: number
  left: number
  maxHeight: number
  side: 'top' | 'bottom'
}

export const FLOAT_GAP = 8
export const FLOAT_MARGIN = 8
export const FLOAT_CAP = 560

/** Where a floating element goes (viewport coordinates). Pure: tested on its own. */
export function placeFloating({ anchor, size, viewport, ceiling, floor, placement, gap = FLOAT_GAP, margin = FLOAT_MARGIN, cap = FLOAT_CAP, insets }: FloatInput): FloatResult {
  const below = Math.max(0, floor - anchor.bottom - gap - margin)
  const above = Math.max(0, anchor.top - ceiling - gap - margin)
  const wantTop = placement.startsWith('top')
  const h = size.height
  let side: 'top' | 'bottom'
  if (wantTop ? h <= above : h <= below) side = wantTop ? 'top' : 'bottom'
  else if (wantTop ? h <= below : h <= above) side = wantTop ? 'bottom' : 'top'
  else side = above > below ? 'top' : 'bottom'
  const room = side === 'top' ? above : below
  const maxHeight = Math.max(0, Math.min(room, cap))
  const shown = Math.min(h, maxHeight)
  const top = side === 'top' ? anchor.top - gap - shown : anchor.bottom + gap
  const wanted = placement.endsWith('end') ? anchor.right - size.width : anchor.left
  const start = Math.max(margin, insets?.left ?? 0)
  const end = viewport.width - Math.max(margin, insets?.right ?? 0)
  const left = Math.max(start, Math.min(wanted, end - size.width))
  return { top, left, maxHeight, side }
}

/** A bar's box when it is on screen (a hidden or slid-away bar is no edge). */
function barBox(sel: string): DOMRect | null {
  const el = document.querySelector(sel)
  const r = el?.getBoundingClientRect()
  return r && r.height > 0 && r.bottom > 0 && r.top < window.innerHeight ? r : null
}

/** The vertical band a menu opened from `trigger` may use. */
export function floatingBand(trigger: Element): { ceiling: number; floor: number } {
  if (trigger.closest('[role="dialog"],[role="alertdialog"]')) {
    const safe = safeInsets()
    return { ceiling: safe.top, floor: window.innerHeight - safe.bottom }
  }
  const top = barBox('[data-top-bar]')
  const bottom = barBox('[data-bottom-bar]')
  // a top bar lower than the trigger (scrolled oddly, a sticky header inside the page) is no ceiling
  const t = trigger.getBoundingClientRect()
  const safe = safeInsets()
  return {
    ceiling: top && top.bottom <= t.top ? top.bottom : safe.top,
    floor: bottom && bottom.top >= t.bottom ? bottom.top : window.innerHeight - safe.bottom,
  }
}

/** The window's safe-area insets, 0 where there are none. The --safe-*
 *  tokens first (Chromium resolves env() inside a custom property); where
 *  a browser keeps the raw «env(…)» text there (WebKit), a hidden probe's
 *  padding of env(…) gives the numbers. */
export function safeInsets(): { top: number; right: number; bottom: number; left: number } {
  const cs = getComputedStyle(document.documentElement)
  const names = ['--safe-top', '--safe-right', '--safe-bottom', '--safe-left'] as const
  const raw = names.map((n) => cs.getPropertyValue(n).trim())
  if (raw.every((v) => v === '' || /^-?[\d.]+px$/.test(v))) {
    const [top, right, bottom, left] = raw.map((v) => parseFloat(v) || 0) as [number, number, number, number]
    return { top, right, bottom, left }
  }
  const p = getComputedStyle(safeAreaProbe())
  const px = (v: string) => parseFloat(v) || 0
  return { top: px(p.paddingTop), right: px(p.paddingRight), bottom: px(p.paddingBottom), left: px(p.paddingLeft) }
}

let probe: HTMLElement | null = null
function safeAreaProbe(): HTMLElement {
  if (probe?.isConnected) return probe
  probe = document.createElement('div')
  probe.dataset.safeAreaProbe = ''
  probe.setAttribute('aria-hidden', 'true')
  probe.style.cssText =
    'position:fixed;top:0;left:0;width:0;height:0;visibility:hidden;pointer-events:none;' +
    'padding:env(safe-area-inset-top) env(safe-area-inset-right) env(safe-area-inset-bottom) env(safe-area-inset-left)'
  document.body.appendChild(probe)
  return probe
}

/**
 * Where a portal goes: the nearest layer of its own around the trigger
 * (`data-portal-host`: the video player, which stands above the page's
 * layers, full screen or not), else the full-screen element that holds the
 * trigger, else <body>. A menu must render in the layer its trigger is in,
 * or that layer covers it.
 */
export function portalHost(trigger: Element | null): HTMLElement {
  const layer = trigger?.closest('[data-portal-host]')
  if (layer instanceof HTMLElement) return layer
  const fs = document.fullscreenElement
  return fs instanceof HTMLElement && trigger && fs.contains(trigger) ? fs : document.body
}

/**
 * The viewport position of the box that `position: fixed` resolves against
 * inside `el`'s parent: the window's (0, 0), unless a layer with a transform
 * or a filter holds the portal.
 */
function fixedOrigin(el: HTMLElement): { x: number; y: number } {
  const host = el.parentElement
  if (!host || host === document.body) return { x: 0, y: 0 }
  const probe = document.createElement('span')
  probe.style.cssText = 'position:fixed;top:0;left:0;width:0;height:0;visibility:hidden'
  host.appendChild(probe)
  const r = probe.getBoundingClientRect()
  probe.remove()
  return { x: r.left, y: r.top }
}

/**
 * Places `el` (position: fixed) next to `trigger`. Writes the style
 * directly: called before the first paint and on every scroll or resize.
 */
export function positionFloating(trigger: HTMLElement, el: HTMLElement, placement: Placement, cap?: number): FloatResult {
  const { ceiling, floor } = floatingBand(trigger)
  // measure at its natural height: a cut from the last placement must not stick
  el.style.maxHeight = ''
  const r = placeFloating({
    anchor: trigger.getBoundingClientRect(),
    size: { width: el.offsetWidth, height: el.offsetHeight },
    viewport: { width: window.innerWidth, height: window.innerHeight },
    ceiling,
    floor,
    placement,
    cap,
    insets: safeInsets(),
  })
  const o = fixedOrigin(el)
  el.style.top = `${r.top - o.y}px`
  el.style.left = `${r.left - o.x}px`
  el.style.maxHeight = `${r.maxHeight}px`
  el.style.transformOrigin = `${placement.endsWith('end') ? 'right' : 'left'} ${r.side === 'top' ? 'bottom' : 'top'}`
  el.dataset.side = r.side
  return r
}

/** Scrolls a scrolling menu so its chosen item is in view (centred when it was out of view). */
export function revealChosen(el: HTMLElement, chosen: HTMLElement | null) {
  if (!chosen || el.scrollHeight <= el.clientHeight) return
  const top = chosen.offsetTop
  const bottom = top + chosen.offsetHeight
  if (top >= el.scrollTop && bottom <= el.scrollTop + el.clientHeight) return
  el.scrollTop = Math.max(0, top - (el.clientHeight - chosen.offsetHeight) / 2)
}
