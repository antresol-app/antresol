// The edition flag marks what only the full build has. Watching titles
// online is not one of those things any more: it is an addon's, so no site,
// stub or route serves it. What is left of the flag is the
// full build's other features (the import's strings, the channel editor's
// row). Every source file that names the flag, and every stub, is listed
// below with what it serves; a new one fails here.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const SRC = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const WEB = path.resolve(SRC, '..')
const FLAG = '__LIBRARY_EDITION__'

type Site =
  // the flag's own declaration, the stubs' and the catalog's notes
  | { note: true }
  // the two catalogs merge the full build's strings: what each line must be
  | { catalog: RegExp }

/** Each file that names the flag, and what it serves. */
const SITES: Record<string, Site> = {
  'vite-env.d.ts': { note: true },
  'i18n/ru/full.ts': { note: true },
  'i18n/ru/full.stub.ts': { note: true },
  'i18n/en/full.stub.ts': { note: true },
  'i18n/ru.ts': { catalog: /\? \(\{\} as typeof full\) : full/ },
  'i18n/en.ts': { catalog: /\? \(\{\} as typeof full\) : full/ },
}

/** Every stub, and what its real module is for. */
const STUBS: Record<string, string> = {
  // the keys of the full build's catalog that core code names
  'i18n/ru/full.stub.ts': 'the import’s strings',
  'i18n/en/full.stub.ts': 'the import’s strings',
  'pages/tv/ChannelExtrasRow.stub.tsx': 'the channel editor’s row',
}

/** The real module of a stub: X.stub.tsx → X.tsx (or .ts). */
const realOf = (stub: string) => [stub.replace(/\.stub\.(tsx?)$/, '.$1'), stub.replace(/\.stub\.tsx?$/, '.ts'), stub.replace(/\.stub\.tsx?$/, '.tsx')]

// the edition is the tree's: the full build's modules sit beside their stubs, the public tree has none of them
const MARKER = 'api/clientFull.ts'
const VAULT = existsSync(path.join(SRC, MARKER))

function sources(dir: string, out: string[] = []): string[] {
  for (const n of readdirSync(dir)) {
    const p = path.join(dir, n)
    if (statSync(p).isDirectory()) {
      if (n !== '__tests__' && n !== 'vendor') sources(p, out)
    } else if (/\.tsx?$/.test(n) && !/\.test\.tsx?$/.test(n)) out.push(p)
  }
  return out
}

const rel = (p: string) => path.relative(SRC, p).split(path.sep).join('/')
const all = sources(SRC).map(rel).sort()

const flagged = all.filter((f) => readFileSync(path.join(SRC, f), 'utf8').includes(FLAG))

const flagLines = (f: string) =>
  readFileSync(path.join(SRC, f), 'utf8')
    .split('\n')
    .filter((l) => l.includes(FLAG))

const isNote = (l: string) => /^\s*(\/\/|\/?\*)/.test(l) || /^declare const __LIBRARY_EDITION__/.test(l)

describe('edition flag', () => {
  it('is named only by the listed files', () => {
    expect(flagged.filter((f) => !(f in SITES))).toEqual([])
  })

  it('is only a note, or the catalog merge, where it is listed', () => {
    for (const [f, site] of Object.entries(SITES)) {
      if (!flagged.includes(f)) continue
      const code = flagLines(f).filter((l) => !isNote(l))
      if ('note' in site) {
        expect(code, f).toEqual([])
        continue
      }
      expect(code.length, f).toBeGreaterThan(0)
      expect(
        code.filter((l) => !site.catalog.test(l)),
        f,
      ).toEqual([])
    }
  })

  it('leaves only the listed stubs', () => {
    const stubs = all.filter((f) => /\.stub\.tsx?$/.test(f))
    expect(stubs.filter((f) => !(f in STUBS))).toEqual([])
    expect(Object.keys(STUBS).filter((f) => !stubs.includes(f))).toEqual([])
    if (!VAULT) return
    // the real module is still in the full build (not gone to an addon)
    for (const f of stubs) expect(realOf(f).some((r) => existsSync(path.join(SRC, r))), f).toBe(true)
  })

  it('has no site, stub, route or module for watching titles online', () => {
    // no source file is named for it
    expect(all.filter((f) => /online/i.test(path.basename(f)))).toEqual([])
    expect(readdirSync(path.join(SRC, 'pages')).filter((n) => /online/i.test(n))).toEqual([])
    // the routes know no full-window player of the full build
    const routes = readFileSync(path.join(SRC, 'app/routes.tsx'), 'utf8')
    expect(routes).not.toMatch(/fullPlayerRoutes|routesFull/)
  })

  it('is decided by the same marker file in both configs', () => {
    for (const cfg of ['vite.config.ts', 'vite.sw.config.ts']) {
      expect(readFileSync(path.join(WEB, cfg), 'utf8'), cfg).toContain(`src/${MARKER}`)
    }
  })

  it('leaves no follow route in routes.tsx', () => {
    const routes = readFileSync(path.join(SRC, 'app/routes.tsx'), 'utf8')
    expect(routes).not.toMatch(/FullSectionPrefetch|fullNoKidsRoutes|fullManageRoutes|follows/)
  })
})
