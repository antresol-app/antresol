// The ratchet the design guards share: count what the rules forbid per file,
// compare with ratchet.json (the baseline of what is still there), fail when a
// count rises or a file that had none gets one, and say so when a count falls
// (lower the baseline). The baseline only shrinks, so what the rules forbid
// disappears one file at a time and never comes back.
//
// The baseline of the app's own files is ratchet.json beside this file. The
// addons' files are in addons/design-ratchet.json, which only the vault has:
// the public core ships no addon, and its baseline would name them.
//
//   UPDATE_RATCHET=1      rewrite the baseline from the tree; refused while any count is higher than
//                         the baseline's (that is a new violation, not a lowering)
//   UPDATE_RATCHET=force  rewrite it whatever it says (the first run, a file that moved)
//   run it with --no-file-parallelism: both guards write the same files
import { existsSync, readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { expect } from 'vitest'

const HERE = path.dirname(fileURLToPath(import.meta.url))
/** web/ */
export const WEB = path.resolve(HERE, '../../..')
/** The repository's root; a file is named in the baseline by its path from here. */
export const ROOT = path.resolve(WEB, '..')
const BASELINE = path.join(HERE, 'ratchet.json')
const ADDONS = path.join(ROOT, 'addons', 'design-ratchet.json')
const isAddon = (file: string) => file.startsWith('addons/')

export type Counts = Record<string, Record<string, number>>
export type Section = 'css' | 'controls'

/** The trees to walk: the app's own, and every addon's web/src (only the vault has those). */
export function sourceRoots(): string[] {
  const out = [path.join(WEB, 'src')]
  const addons = path.join(ROOT, 'addons')
  if (existsSync(addons)) {
    for (const a of readdirSync(addons).sort()) {
      const d = path.join(addons, a, 'web', 'src')
      if (existsSync(d)) out.push(d)
    }
  }
  return out
}

/** Every file in `dirs` that `match` takes, tests and dependencies left out; paths from ROOT. */
export function sourceFiles(match: (name: string) => boolean): string[] {
  const out: string[] = []
  const walk = (dir: string) => {
    for (const name of readdirSync(dir).sort()) {
      const p = path.join(dir, name)
      if (statSync(p).isDirectory()) {
        if (name !== 'node_modules' && name !== '__tests__' && name !== 'dist') walk(p)
      } else if (match(name)) out.push(path.relative(ROOT, p).split(path.sep).join('/'))
    }
  }
  for (const r of sourceRoots()) walk(r)
  return out
}

export const read = (rel: string) => readFileSync(path.join(ROOT, rel), 'utf8')

function readBaseline(file: string): Partial<Record<Section, Counts>> {
  return existsSync(file) ? (JSON.parse(readFileSync(file, 'utf8')) as Partial<Record<Section, Counts>>) : {}
}

/** The app's baseline and the addons' (when the tree has them), one count table per section. */
function load(): Partial<Record<Section, Counts>> {
  const core = readBaseline(BASELINE)
  const addons = readBaseline(ADDONS)
  const out: Partial<Record<Section, Counts>> = {}
  for (const s of new Set([...Object.keys(core), ...Object.keys(addons)]) as Set<Section>) out[s] = { ...core[s], ...addons[s] }
  return out
}

/** One file per line, sorted: a wave that lowers its files' counts edits only those lines. */
function dump(all: Partial<Record<Section, Counts>>): string {
  const sections = (Object.keys(all) as Section[]).sort().map((s) => {
    const files = Object.keys(all[s]!).sort()
    const lines = files.map((f) => `    ${JSON.stringify(f)}: ${JSON.stringify(all[s]![f])}`)
    return `  ${JSON.stringify(s)}: {\n${lines.join(',\n')}\n  }`
  })
  return `{\n${sections.join(',\n')}\n}\n`
}

const entries = (c: Counts) => Object.entries(c).flatMap(([f, rules]) => Object.entries(rules).map(([r, n]) => ({ f, r, n })))

/** Counts that are higher than the baseline's (a missing entry is zero). */
function rises(base: Counts, now: Counts) {
  return entries(now).filter(({ f, r, n }) => n > (base[f]?.[r] ?? 0)).map(({ f, r, n }) => `${f}: ${r} ${base[f]?.[r] ?? 0} -> ${n}`)
}

/** The file's counts without the zeros. */
export function nonZero(counts: Record<string, number>): Record<string, number> {
  return Object.fromEntries(Object.entries(counts).filter(([, n]) => n > 0))
}

export function ratchet(section: Section, found: Counts, what: string) {
  const all = load()
  const base = all[section] ?? {}
  const up = rises(base, found)
  const mode = process.env.UPDATE_RATCHET
  if (mode) {
    if (up.length > 0 && mode !== 'force' && Object.keys(base).length > 0) {
      throw new Error(`ratchet: ${section} would rise, not fall (new ${what}, or a moved file: UPDATE_RATCHET=force):\n${up.join('\n')}`)
    }
    const split = (own: boolean) => Object.fromEntries(Object.entries(found).filter(([f]) => isAddon(f) === own))
    writeFileSync(BASELINE, dump({ ...readBaseline(BASELINE), [section]: split(false) }))
    // a tree without addons has no baseline for them: never write an empty file there
    if (existsSync(path.join(ROOT, 'addons'))) writeFileSync(ADDONS, dump({ ...readBaseline(ADDONS), [section]: split(true) }))
    return
  }
  expect(up, `${what}: more than ratchet.json allows. Use the tokens and the kit (web/DESIGN.md); a deliberate exception on one declaration says why with a trailing comment, /* ds-allow: art */`).toEqual([])
  const down = entries(base).filter(({ f, r, n }) => (found[f]?.[r] ?? 0) < n)
  if (down.length > 0) {
    console.warn(`ratchet (${section}): ${down.length} counts fell (${down.slice(0, 3).map(({ f, r }) => `${f} ${r}`).join('; ')}${down.length > 3 ? '; …' : ''}). Lower the baseline: UPDATE_RATCHET=1 npx vitest run src/design --no-file-parallelism`)
  }
}
