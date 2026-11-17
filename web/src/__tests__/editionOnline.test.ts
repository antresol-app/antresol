// The edition flag is left only where the sources build has online viewing
// (nothing else). Every other place asks a slot (what only some servers have
// comes from an addon). The list below is every source file that names the
// flag; a new one fails here. The same goes for the stubs (X.stub.ts(x)
// beside the sources build's X): each is online viewing's; a stub whose real
// module left for an addon fails here.
import { existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const SRC = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const FLAG = '__LIBRARY_EDITION__'

type Site =
  // online viewing of the sources build: what each line must name
  | { online: RegExp }
  // the flag's own declaration, the stubs' and catalogs' notes
  | { note: true }
  // the two catalogs merge the sources build's strings: what each line must be
  | { catalog: RegExp }

const SITES: Record<string, Site> = {
  'api/client.ts': { online: /online: __LIBRARY_EDITION__ \? undefined : 1/ },
  'app/routes.tsx': { online: /fullPlayerRoutes\(\)/ },
  'components/Card.tsx': { online: /\.online\b/ },
  'pages/manage/Settings.tsx': { online: /<OnlineRow\b/ },
  'vite-env.d.ts': { note: true },
  'app/routesFull.tsx': { note: true },
  'app/routesFull.stub.tsx': { note: true },
  'pages/ItemDetailFull.tsx': { note: true },
  'i18n/ru/full.ts': { note: true },
  'i18n/ru/full.stub.ts': { note: true },
  'i18n/en/full.stub.ts': { note: true },
  'i18n/ru.ts': { catalog: /\? \(\{\} as typeof full\) : full/ },
  'i18n/en.ts': { catalog: /\? \(\{\} as typeof full\) : full/ },
}

/** Every stub, and whose its real module is. */
const STUBS: Record<string, 'online'> = {
  'app/routesFull.stub.tsx': 'online',
  'components/episodes/EpisodePlay.stub.tsx': 'online',
  // the sources build's keys that the core names
  'i18n/ru/full.stub.ts': 'online',
  'i18n/en/full.stub.ts': 'online',
  'pages/ItemDetailFull.stub.tsx': 'online',
  'pages/tv/ChannelExtrasRow.stub.tsx': 'online',
}

/** The real module of a stub: X.stub.tsx → X.tsx (or .ts). */
const realOf = (stub: string) => [stub.replace(/\.stub\.(tsx?)$/, '.$1'), stub.replace(/\.stub\.tsx?$/, '.ts'), stub.replace(/\.stub\.tsx?$/, '.tsx')]

// vault has the sources build's modules beside the stubs; the public tree has none of them
const VAULT = existsSync(path.join(SRC, 'api/clientFull.ts'))

function sources(dir: string, out: string[] = []): string[] {
  for (const n of readdirSync(dir)) {
    const p = path.join(dir, n)
    if (statSync(p).isDirectory()) {
      if (n !== '__tests__' && n !== 'vendor') sources(p, out)
    } else if (/\.tsx?$/.test(n) && !/\.test\.tsx?$/.test(n)) out.push(p)
  }
  return out
}

const flagged = sources(SRC)
  .filter((p) => readFileSync(p, 'utf8').includes(FLAG))
  .map((p) => path.relative(SRC, p).split(path.sep).join('/'))
  .sort()

const flagLines = (rel: string) =>
  readFileSync(path.join(SRC, rel), 'utf8')
    .split('\n')
    .filter((l) => l.includes(FLAG))

const isNote = (l: string) => /^\s*(\/\/|\/?\*)/.test(l) || /^declare const __LIBRARY_EDITION__/.test(l)

describe('edition flag: online only', () => {
  it('is named only by the listed files', () => {
    expect(flagged.filter((f) => !(f in SITES))).toEqual([])
  })

  it('is its listed site wherever it is code in an online or catalog file', () => {
    const pattern = (site: Site): RegExp | null => ('online' in site ? site.online : 'catalog' in site ? site.catalog : null)
    for (const [f, site] of Object.entries(SITES)) {
      const re = pattern(site)
      if (!re || !flagged.includes(f)) continue
      const code = flagLines(f).filter((l) => !isNote(l))
      expect(code.length, f).toBeGreaterThan(0)
      expect(
        code.filter((l) => !re.test(l)),
        f,
      ).toEqual([])
    }
  })

  it('is only a note in the note files', () => {
    for (const [f, site] of Object.entries(SITES)) {
      if (!('note' in site) || !flagged.includes(f)) continue
      expect(
        flagLines(f).filter((l) => !isNote(l)),
        f,
      ).toEqual([])
    }
  })

  it('leaves only online\'s stubs', () => {
    const stubs = sources(SRC)
      .map((p) => path.relative(SRC, p).split(path.sep).join('/'))
      .filter((f) => /\.stub\.tsx?$/.test(f))
      .sort()
    expect(stubs.filter((f) => !(f in STUBS))).toEqual([])
    expect(Object.keys(STUBS).filter((f) => !stubs.includes(f))).toEqual([])
    if (!VAULT) return
    // the real module is still in vault (not gone to an addon)
    for (const f of stubs) expect(realOf(f).some((r) => existsSync(path.join(SRC, r))), f).toBe(true)
  })

  it('leaves no follow route in routes.tsx', () => {
    const routes = readFileSync(path.join(SRC, 'app/routes.tsx'), 'utf8')
    expect(routes).not.toMatch(/FullSectionPrefetch|fullNoKidsRoutes|fullManageRoutes|follows/)
  })
})
