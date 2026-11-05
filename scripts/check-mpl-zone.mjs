// SPDX-License-Identifier: AGPL-3.0-or-later
// Copyright (c) 2026 Antresol Contributors
//
// The MPL gate. The native app's reader bundle (web/reader-native.html and
// what it imports) ships inside the closed App Store app, so every module of
// it must be MPL-2.0, MIT or BSD: never AGPL code (LICENSING.md, "MPL-2.0
// zones").
//
//   node scripts/check-mpl-zone.mjs [REPO_ROOT]
//
// 1. Builds the reader closure in memory with web/vite.reader-native.config.ts
//    (build.write false, source maps on; the config's own output plugin, which
//    empties the app's reader folder, is left out) and lists every module.
// 2. Looks each module up in the zone table of LICENSING.md (between the
//    `mpl-zones` markers; the most specific row wins; no row = AGPL) or, for an
//    npm package, in its package.json. Anything not MPL/MIT/BSD fails.
// 3. Checks the zone table itself: every listed file exists; every MPL file
//    row carries `SPDX-License-Identifier: MPL-2.0`; every MPL directory row
//    has a LICENSE file; and no file under web/ outside the table claims MPL.
//
// Exit 0 when clean, 1 on a finding, 2 on a usage or build error.
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { createRequire } from 'node:module'
import { tmpdir } from 'node:os'
import { dirname, join, relative, resolve, sep } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const root = resolve(process.argv[2] ?? join(dirname(fileURLToPath(import.meta.url)), '..'))
const web = join(root, 'web')
const ALLOWED = new Set(['MPL-2.0', 'MIT', 'BSD-2-Clause', 'BSD-3-Clause', '0BSD'])
const SPDX_MPL = /SPDX-License-Identifier:\s*MPL-2\.0\b/
const problems = []
const rel = (p) => relative(root, p).split(sep).join('/')

// --- the zone table ---------------------------------------------------------
const licensing = join(root, 'LICENSING.md')
if (!existsSync(licensing)) {
  console.error('check-mpl-zone: no LICENSING.md')
  process.exit(2)
}
const md = readFileSync(licensing, 'utf8')
const begin = md.indexOf('<!-- mpl-zones:begin -->')
const end = md.indexOf('<!-- mpl-zones:end -->')
if (begin < 0 || end < begin) {
  console.error('check-mpl-zone: LICENSING.md has no <!-- mpl-zones:begin/end --> table')
  process.exit(2)
}
const zones = []
for (const line of md.slice(begin, end).split('\n')) {
  const m = /^\|\s*`([^`]+)`\s*\|\s*([A-Za-z0-9.+-]+)\s*\|/.exec(line)
  if (m) zones.push({ pattern: m[1], license: m[2] })
}
if (!zones.length) {
  console.error('check-mpl-zone: the zone table in LICENSING.md is empty')
  process.exit(2)
}
const isDirRule = (p) => p.endsWith('/**')
const matches = (z, path) => (isDirRule(z.pattern) ? path.startsWith(z.pattern.slice(0, -2)) : path === z.pattern)
// Exact file rows beat directory rows; a deeper directory beats a shallower one.
const specificity = (z) => (isDirRule(z.pattern) ? z.pattern.length : 1e6)
function zoneOf(path) {
  let best = null
  for (const z of zones) if (matches(z, path) && (!best || specificity(z) > specificity(best))) best = z
  return best
}
const head = (file) => readFileSync(file, 'utf8').split('\n').slice(0, 12).join('\n')

for (const z of zones) {
  const p = join(root, isDirRule(z.pattern) ? z.pattern.slice(0, -3) : z.pattern)
  if (!existsSync(p)) {
    problems.push(`LICENSING.md lists ${z.pattern}, which does not exist`)
    continue
  }
  if (z.license !== 'MPL-2.0') continue
  if (isDirRule(z.pattern)) {
    if (!existsSync(join(p, 'LICENSE'))) problems.push(`${z.pattern} is MPL-2.0 by directory, but ${rel(join(p, 'LICENSE'))} is missing`)
  } else if (!SPDX_MPL.test(head(p))) {
    problems.push(`${z.pattern} is MPL-2.0 in LICENSING.md but has no "SPDX-License-Identifier: MPL-2.0" line at the top`)
  }
}

// No file outside the table may claim MPL (the table is the zone list).
function walk(dir, out) {
  for (const n of readdirSync(dir)) {
    if (n === 'node_modules' || n === 'dist' || n.startsWith('.')) continue
    const p = join(dir, n)
    if (statSync(p).isDirectory()) walk(p, out)
    else if (/\.(ts|tsx|js|mjs|html|css)$/.test(n)) out.push(p)
  }
  return out
}
for (const f of walk(web, [])) {
  const r = rel(f)
  if (SPDX_MPL.test(head(f)) && zoneOf(r)?.license !== 'MPL-2.0') {
    problems.push(`${r} says MPL-2.0 but LICENSING.md does not list it in an MPL-2.0 zone`)
  }
}

// --- the reader closure -----------------------------------------------------
const req = createRequire(join(web, 'package.json'))
let vite
try {
  vite = await import(pathToFileURL(req.resolve('vite')).href)
} catch (e) {
  console.error(`check-mpl-zone: cannot load vite from web/node_modules (${e.message}); run npm --prefix web ci`)
  process.exit(2)
}
const configFile = join(web, 'vite.reader-native.config.ts')
const loaded = await vite.loadConfigFromFile({ command: 'build', mode: 'production' }, configFile, web, 'silent')
if (!loaded) {
  console.error(`check-mpl-zone: cannot load ${rel(configFile)}`)
  process.exit(2)
}
const config = loaded.config
const outDir = join(tmpdir(), 'check-mpl-zone-never-written')
let output
try {
  output = await vite.build({
    ...config,
    configFile: false,
    root: web,
    logLevel: 'silent',
    // the config's output plugin empties the app's reader folder: never run it
    plugins: (config.plugins ?? []).flat().filter((p) => p && p.name !== 'vault-native-reader-output'),
    build: { ...config.build, outDir, write: false, sourcemap: true, emptyOutDir: false },
  })
} catch (e) {
  console.error(`check-mpl-zone: the reader closure does not build: ${e.message}`)
  process.exit(2)
}
const chunks = (Array.isArray(output) ? output : [output]).flatMap((o) => o.output)
const modules = new Set()
for (const c of chunks) {
  if (c.type !== 'chunk') continue
  for (const id of Object.keys(c.modules)) if (!id.startsWith('\0')) modules.add(id.split('?')[0])
  for (const s of c.map?.sources ?? []) {
    const p = resolve(dirname(join(outDir, c.fileName)), s)
    if (!p.includes('\0')) modules.add(p)
  }
}
if (!modules.size) {
  console.error('check-mpl-zone: the reader closure has no modules (no source maps?)')
  process.exit(2)
}

function npmLicense(file) {
  for (let d = dirname(file); d.startsWith(web); d = dirname(d)) {
    const pj = join(d, 'package.json')
    if (existsSync(pj) && relative(join(web, 'node_modules'), d).split(sep).length <= 2) {
      const j = JSON.parse(readFileSync(pj, 'utf8'))
      if (j.name) return { name: j.name, license: typeof j.license === 'string' ? j.license : (j.license?.type ?? '') }
    }
  }
  return { name: '?', license: '' }
}

let own = 0
let vendor = 0
let npm = 0
// The page itself is the entry; it is checked on its own below.
const list = [...modules].filter((m) => !m.endsWith('.html')).sort()
for (const m of list) {
  const r = rel(m)
  if (m.split(sep).includes('node_modules')) {
    const { name, license } = npmLicense(m)
    npm++
    // "(MIT OR Apache-2.0)" style expressions: every alternative must be fine on its own,
    // or at least one in an OR
    const ok = license.split(/\s+OR\s+/).some((alt) => alt.replace(/[()]/g, '').split(/\s+AND\s+/).every((x) => ALLOWED.has(x.trim())))
    if (!ok) problems.push(`${r}: npm package ${name} is ${license || 'unlicensed'}, not MPL/MIT/BSD`)
    continue
  }
  const z = zoneOf(r)
  const license = z?.license ?? 'AGPL-3.0-or-later'
  if (!ALLOWED.has(license)) {
    problems.push(`${r} is in the reader closure but is ${license}${z ? '' : ' (no MPL/MIT/BSD zone in LICENSING.md)'}`)
    continue
  }
  if (license === 'MPL-2.0') own++
  else vendor++
}
// Type-only imports are erased from the bundle, so the build above does not
// see them, yet an MPL source that names an AGPL type still depends on it:
// every relative import or re-export in a closure source must be MPL/MIT/BSD too.
const resolveImport = (from, spec) => {
  const p = resolve(dirname(from), spec)
  const cands = [p, `${p}.ts`, `${p}.tsx`, `${p}.d.ts`, `${p}.js`, join(p, 'index.ts'), p.replace(/\.js$/, '.ts'), p.replace(/\.js$/, '.d.ts')]
  return cands.find((c) => existsSync(c) && statSync(c).isFile())
}
for (const m of list) {
  if (m.split(sep).includes('node_modules') || !/\.(ts|tsx|js|mjs)$/.test(m) || !existsSync(m)) continue
  const src = readFileSync(m, 'utf8')
  for (const [, spec] of src.matchAll(/^\s*(?:import|export)\b[^;'"]*?\bfrom\s*['"](\.{1,2}\/[^'"]+)['"]/gm)) {
    const target = resolveImport(m, spec)
    if (!target) {
      problems.push(`${rel(m)} imports ${spec}, which does not resolve to a file`)
      continue
    }
    const license = zoneOf(rel(target))?.license ?? 'AGPL-3.0-or-later'
    if (!ALLOWED.has(license)) problems.push(`${rel(m)} imports ${rel(target)} (${license}; a type-only import counts too)`)
  }
}
const entry = 'web/reader-native.html'
if (zoneOf(entry)?.license !== 'MPL-2.0') problems.push(`${entry} is the reader's entry but is not MPL-2.0 in LICENSING.md`)

// CHECK_MPL_ZONE_LIST=1 prints the closure.
if (process.env.CHECK_MPL_ZONE_LIST) for (const m of list) console.error(`  ${rel(m)}`)
if (problems.length) {
  for (const p of problems) console.error(`check-mpl-zone: ${p}`)
  console.error(`check-mpl-zone: FAILED, ${problems.length} finding(s); ${list.length} modules in the reader closure`)
  process.exit(1)
}
console.log(`check-mpl-zone: ok, ${list.length} modules (${own} MPL-2.0, ${vendor} vendored MIT/BSD${npm ? `, ${npm} npm` : ''}) + ${entry}`)
