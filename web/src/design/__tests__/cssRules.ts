// What a module's CSS may not write: the design system's rule 1 (web/DESIGN.md),
// as one count per rule, over a parsed stylesheet (comments, nesting and @media
// need no regex tricks). tokensOnly.test.ts compares the counts with the baseline.
import postcss, { type AtRule, type ChildNode, type Declaration, type Rule } from 'postcss'

export const CSS_RULES = ['colour', 'type', 'radius', 'shadow', 'weight', 'zindex', 'motion', 'space', 'control', 'spinKeyframes'] as const
export type CssRule = (typeof CSS_RULES)[number]

/** The control heights and widths of the token scale (30 36 44 52 48) and the off-grid sizes beside it (32 40 46). */
const CONTROL_SIZES = new Set([30, 32, 36, 40, 44, 46, 48, 52])

/** A value without its `var(--token, fallback)` parts and its urls: what is left is written by hand. */
function bare(value: string): string {
  let v = value.replace(/url\([^)]*\)/g, '')
  for (let prev = ''; prev !== v; ) {
    prev = v
    v = v.replace(/var\([^()]*\)/g, '')
  }
  return v
}

const lengths = (v: string, units: string) => [...v.matchAll(new RegExp(`(-?\\d*\\.?\\d+)(${units})\\b`, 'g'))].map((m) => parseFloat(m[1]!))
const literalLength = (v: string, units = 'px|rem|em') => lengths(v, units).some((n) => n !== 0)

const COLOUR = /#[0-9a-f]{3,8}\b|\b(?:rgba?|hsla?)\(/i

/** The rule a declaration breaks, if it breaks one. */
const BREAKS: Record<Exclude<CssRule, 'spinKeyframes'>, (prop: string, value: string) => boolean> = {
  colour: (prop, v) => !/^(box|text)-shadow$/.test(prop) && COLOUR.test(bare(v)),
  type: (prop, v) => (prop === 'font' || prop === 'font-size') && literalLength(bare(v), 'px|rem'),
  radius: (prop, v) => /radius$/.test(prop) && literalLength(bare(v)) && !/\b9{3,}px/.test(v),
  shadow: (prop, v) => /^(box|text)-shadow$/.test(prop) && COLOUR.test(bare(v)),
  weight: (prop, v) => prop === 'font-weight' && /^(\d{3}|bold|bolder)$/.test(v.trim()),
  zindex: (prop, v) => prop === 'z-index' && /^-?\d+$/.test(v.trim()) && v.trim() !== '0',
  motion: (prop, v) => /^(transition|animation)(-duration|-delay)?$/.test(prop) && lengths(bare(v), 'm?s').some((n) => n !== 0),
  space: (prop, v) => /^(padding|margin)(-(top|right|bottom|left|block|inline)(-(start|end))?)?$|^(row-|column-)?gap$/.test(prop) && lengths(bare(v), 'px').some((n) => Math.abs(n) > 2),
  control: (prop, v) => /^(min-|max-)?(height|width)$/.test(prop) && /^\d+px$/.test(v.trim()) && CONTROL_SIZES.has(parseInt(v, 10)),
}

const ALLOW = /^\s*ds-allow:\s*\S/

/** A trailing `/* ds-allow: why *\/` on the declaration's last line, or one that opens its rule, excuses it. */
function allowed(d: Declaration): boolean {
  const next = d.next()
  if (next?.type === 'comment' && ALLOW.test(next.text) && next.source?.start?.line === d.source?.end?.line) return true
  if (ALLOW.test(d.raws.value?.raw?.match(/\/\*([\s\S]*?)\*\//)?.[1] ?? '')) return true
  const first = d.parent?.first
  return first?.type === 'comment' && ALLOW.test(first.text)
}

/** How many declarations of each rule `css` breaks (the rules it breaks none of are left out). */
export function countCss(css: string, skip: readonly CssRule[] = []): Record<string, number> {
  const counts: Record<string, number> = {}
  const add = (r: CssRule) => {
    if (!skip.includes(r)) counts[r] = (counts[r] ?? 0) + 1
  }
  postcss.parse(css, { from: undefined }).walk((node: ChildNode) => {
    if (node.type === 'atrule' && (node as AtRule).name === 'keyframes' && (node as AtRule).params.trim() === 'spin') add('spinKeyframes')
    if (node.type !== 'decl' || allowed(node)) return
    for (const r of Object.keys(BREAKS) as (keyof typeof BREAKS)[]) if (BREAKS[r](node.prop, node.value)) add(r)
  })
  return counts
}

/** Video chrome measured against the picture, and the design's own files: DESIGN.md's exceptions to rule 1. */
const EXEMPT = [
  /^web\/src\/design\//,
  /^web\/src\/ui\/gallery\//,
  /^web\/src\/player\/(VideoPlayer|NowPlaying|Scrubber|SkipControls|VolumeControl)\.module\.css$/,
  /^web\/src\/reader\/(Reader|ReaderChrome|ComicReader)\.module\.css$/,
  /^web\/src\/pages\/tv\/LivePlayer[^/]*\.module\.css$/,
]

/** The rules a file is checked against: all of them, except in the exempt files, where only a second `@keyframes spin` is still a copy. */
export const skippedFor = (file: string): readonly CssRule[] => (EXEMPT.some((re) => re.test(file)) ? CSS_RULES.filter((r) => r !== 'spinKeyframes') : [])

export type { Rule }
