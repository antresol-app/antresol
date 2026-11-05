// Writes THIRD_PARTY_LICENSES.md (Markdown, on stdout) for the library
// edition of the server: every Go module in `go list -deps
// ./cmd/vault`, the Go standard library, every npm package whose code is in
// the library web bundle (read from that build's source maps) and the code
// vendored in web/src/vendor — each with its licence's full text.
//
//   node scripts/third-party-licenses.mjs <dir of a sourcemapped library web build>
//
// scripts/third-party-licenses.sh makes that build and calls this.
import { execFileSync } from 'node:child_process'
import { existsSync, readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const webBuild = process.argv[2]
if (!webBuild) {
  console.error('usage: node scripts/third-party-licenses.mjs <web build dir with source maps>')
  process.exit(2)
}

// Only licence files: never READMEs, changelogs or third-party notices of
// code that is not in the binary.
const LICENSE_FILE = /^(licen[cs]e|copying|notice|patents|unlicense)([._-][\w.-]*)?$/i

function licenseFiles(dir) {
  if (!dir || !existsSync(dir)) return []
  return readdirSync(dir)
    .filter((n) => LICENSE_FILE.test(n) && !/\.(go|js|ts|json|html)$/i.test(n) && statSync(join(dir, n)).isFile())
    .sort((a, b) => rank(a) - rank(b) || a.localeCompare(b))
    .map((n) => ({ name: n, text: readFileSync(join(dir, n), 'utf8').replace(/\r\n/g, '\n').trim() }))
}

// The licence files of a component, recursively: those at its root and, for
// every directory whose code is in the build (a Go package, a bundled npm
// file's folder), those in that directory and the ones between it and the
// root. A nested file covers code copied in from elsewhere (for example
// klauspost/compress/zstd/internal/xxhash), so it is listed under its path.
function licenseFilesWithin(top, dirs) {
  const out = licenseFiles(top)
  const seen = new Set([top])
  const topSlash = top.endsWith(sep) ? top : top + sep
  for (const d of dirs) {
    for (let x = d; x.startsWith(topSlash) && !seen.has(x); x = dirname(x)) {
      seen.add(x)
      const at = relative(top, x).split(sep).join('/')
      for (const f of licenseFiles(x)) out.push({ ...f, name: `${at}/${f.name}` })
    }
  }
  return out
}

// Licence-named files that are not about code in this binary, left out on
// purpose (the reason is printed in place of the text).
const SKIP = {
  'modernc.org/sqlite': {
    'LICENSE-3RD-PARTY.md':
      'modernc.org/sqlite also ships LICENSE-3RD-PARTY.md, an inventory of its whole module graph, test-only code and ' +
      'repository material. It is not repeated here: what of it is linked into this binary is listed in this file on its ' +
      'own account (the Go modules above, musl libc and the other notices under modernc.org/libc, and SQLite and ' +
      'sqlite-vec below).',
  },
  'modernc.org/memory': { 'LICENSE-LOGO': 'LICENSE-LOGO (the attribution of a logo image that is not in this binary) is left out.' },
}

const rank = (n) => (/^licen/i.test(n) ? 0 : /^copying/i.test(n) ? 1 : /^notice/i.test(n) ? 2 : 3)

/** The SPDX id of a licence text (the common ones; "" if unsure). */
function identify(text) {
  if (/Apache License,?\s+Version 2\.0/i.test(text)) return 'Apache-2.0'
  if (/Mozilla Public License,?\s+(Version|v\.?)\s*2\.0/i.test(text)) return 'MPL-2.0'
  if (/Permission is hereby granted, free of charge/i.test(text)) return 'MIT'
  if (/Permission to use, copy, modify, and\/?or distribute this software for any\s+purpose/i.test(text)) return 'ISC'
  if (/Redistribution and use in source and binary forms/i.test(text)) {
    return /(Neither the name|names of (its|the) contributors|name of the copyright holder)/i.test(text) ? 'BSD-3-Clause' : 'BSD-2-Clause'
  }
  if (/This is free and unencumbered software released into the public domain/i.test(text)) return 'Unlicense'
  return ''
}

function licenseOf(files, declared) {
  const ids = [...new Set(files.filter((f) => /(licen[cs]e|copying|unlicense)/i.test(f.name)).map((f) => identify(f.text)).filter(Boolean))]
  if (ids.length) return ids.join(' AND ')
  return declared || 'see text'
}

// Module paths in proxy URLs: an upper-case letter becomes "!" + lower case.
const escapePath = (p) => p.replace(/[A-Z]/g, (c) => '!' + c.toLowerCase())

function goModules() {
  const out = execFileSync(
    'go',
    ['list', '-deps', '-f', '{{with .Module}}{{.Path}}\t{{.Version}}\t{{.Dir}}\t{{.Main}}\t{{with .Replace}}{{.Path}}{{end}}\t{{$.Dir}}{{end}}', './cmd/vault'],
    // the core build's modules: no GOFLAGS of the caller (vault's GNUmakefile sets the sources build's)
    { cwd: root, encoding: 'utf8', env: { ...process.env, GOFLAGS: '' } },
  )
  const mods = new Map()
  for (const line of out.split('\n')) {
    if (!line.trim()) continue
    const [path, version, dir, main, replace, pkgDir] = line.split('\t')
    if (main === 'true') continue
    if (!mods.has(path)) mods.set(path, { path, version, dir, replace, pkgDirs: [] })
    mods.get(path).pkgDirs.push(pkgDir)
  }
  return [...mods.values()].sort((a, b) => a.path.localeCompare(b.path))
}

function goEntries() {
  const entries = []
  const goroot = execFileSync('go', ['env', 'GOROOT'], { encoding: 'utf8' }).trim()
  const gover = execFileSync('go', ['env', 'GOVERSION'], { encoding: 'utf8' }).trim()
  const std = licenseFiles(goroot)
  entries.push({
    group: 'go',
    name: 'Go standard library and runtime',
    version: gover,
    license: licenseOf(std),
    source: 'https://go.dev/dl/',
    files: std,
  })
  const mods = goModules()
  if (!std.some((f) => /^licen/i.test(f.name))) {
    // some Go toolchain downloads leave LICENSE out of GOROOT; the Go
    // project's LICENSE is the same file in every golang.org/x module
    const x = mods.find((m) => m.path.startsWith('golang.org/x/'))
    const lic = x && licenseFiles(x.dir).find((f) => /^licen/i.test(f.name))
    if (lic) {
      std.unshift(lic)
      entries[0].license = licenseOf(std)
      entries[0].note = `GOROOT has no LICENSE file: this is the Go project's LICENSE as shipped in ${x.path}.`
    } else entries[0].problem = 'no LICENSE in GOROOT'
  }
  for (const m of mods) {
    const skip = SKIP[m.path] ?? {}
    const files = licenseFilesWithin(m.dir, m.pkgDirs).filter((f) => !skip[f.name])
    const e = { group: 'go', name: m.path, version: m.version, files, license: licenseOf(files) }
    const skipped = Object.keys(skip).filter((n) => existsSync(join(m.dir, n)))
    if (skipped.length) e.note = skipped.map((n) => skip[n]).join(' ')
    if (m.replace) {
      // the local fork wired in by go.mod `replace`
      e.note =
        (e.note ? e.note + ' ' : '') +
        `A local copy of ${m.path} ${m.version} (third_party/sevenzip, wired in by a go.mod \`replace\`), ` +
        'patched to bound the allocations made while reading untrusted archive headers; otherwise unchanged. ' +
        'Same licence as upstream.'
      e.source = `https://github.com/bodgit/sevenzip/tree/${m.version}`
    } else {
      e.source = `https://pkg.go.dev/${m.path}@${m.version}`
    }
    if (e.license.includes('MPL-2.0')) {
      // MPL-2.0 §3.2: say where the Source Code Form is available
      e.note = (e.note ? e.note + ' ' : '') + `Source Code Form (MPL-2.0 §3.2), unmodified: https://proxy.golang.org/${escapePath(m.path)}/@v/${m.version}.zip`
    }
    if (!files.length) e.problem = 'no licence file found in the module'
    entries.push(e)
  }
  return entries
}

/** Every source file of the bundle, from its source maps. */
function bundleSources() {
  const out = new Set()
  const walk = (d) => {
    for (const n of readdirSync(d)) {
      const p = join(d, n)
      if (statSync(p).isDirectory()) walk(p)
      else if (n.endsWith('.map')) {
        const map = JSON.parse(readFileSync(p, 'utf8'))
        for (const s of map.sources ?? []) out.add(resolve(dirname(p), map.sourceRoot ?? '', s))
      }
    }
  }
  walk(webBuild)
  return [...out]
}

function npmEntries(sources) {
  const pkgs = new Map()
  const fileDirs = new Map()
  for (const s of sources) {
    const i = s.lastIndexOf(`${sep}node_modules${sep}`)
    if (i < 0) continue
    const rest = s.slice(i + 14).split(sep)
    const name = rest[0].startsWith('@') ? `${rest[0]}/${rest[1]}` : rest[0]
    const dir = s.slice(0, i + 14) + name.split('/').join(sep)
    if (!pkgs.has(dir)) pkgs.set(dir, name)
    if (!fileDirs.has(dir)) fileDirs.set(dir, new Set())
    fileDirs.get(dir).add(dirname(s))
  }
  const entries = []
  for (const [dir, name] of pkgs) {
    const pj = JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8'))
    const files = licenseFilesWithin(dir, [...fileDirs.get(dir)])
    const declared = typeof pj.license === 'string' ? pj.license : pj.license?.type ?? ''
    const repo = typeof pj.repository === 'string' ? pj.repository : pj.repository?.url
    const e = {
      group: 'npm',
      name: pj.name ?? name,
      version: pj.version ?? '',
      files,
      license: declared || licenseOf(files),
      source: `https://www.npmjs.com/package/${pj.name ?? name}/v/${pj.version}`,
    }
    if (repo) e.repository = repo.replace(/^git\+/, '').replace(/\.git$/, '')
    if (!files.length) e.problem = `no licence file in the package; its package.json says ${declared || 'nothing'}`
    entries.push(e)
  }
  return entries.sort((a, b) => a.name.localeCompare(b.name))
}

function vendorEntries(sources) {
  const vendor = join(root, 'web', 'src', 'vendor', 'foliate')
  const used = (p) => sources.some((s) => s.startsWith(p))
  if (!used(vendor + sep)) return []
  const version = readFileSync(join(vendor, 'VERSION'), 'utf8').trim()
  const out = [
    {
      group: 'vendor',
      name: 'foliate-js',
      version: `commit ${version}`,
      files: licenseFiles(vendor),
      license: licenseOf(licenseFiles(vendor)),
      source: `https://github.com/johnfactotum/foliate-js/tree/${version}`,
      note: 'Vendored in web/src/vendor/foliate (the e-book reader); pdf.js there is a local stub and the page turn in paginator.js is changed.',
    },
  ]
  const lib = (file, name, version, src) => {
    if (!used(join(vendor, 'vendor', file))) return
    const files = [{ name: `${file}.LICENSE`, text: readFileSync(join(vendor, 'vendor', `${file}.LICENSE`), 'utf8').trim() }]
    out.push({ group: 'vendor', name, version: `${version} (as bundled by foliate-js)`, files, license: licenseOf(files), source: src, note: `Vendored in web/src/vendor/foliate/vendor/${file}.` })
  }
  // versions: foliate-js's package-lock.json at the vendored commit (see NOTICE)
  lib('zip.js', 'zip.js', '2.8.22', 'https://github.com/gildas-lormeau/zip.js')
  lib('fflate.js', 'fflate', '0.8.2', 'https://github.com/101arrowz/fflate')
  return out
}

const sources = bundleSources()
if (!sources.length) {
  console.error(`third-party-licenses: no source maps in ${webBuild}`)
  process.exit(1)
}
const go = goEntries()
const web = [...npmEntries(sources), ...vendorEntries(sources)]
const all = [...go, ...web]
const problems = all.filter((e) => e.problem)

const anchor = (e) => (e.group + '-' + e.name).toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '')
const fence = (t) => {
  let f = '```'
  while (t.includes(f)) f += '`'
  return `${f}text\n${t}\n${f}`
}
let md = `# Third-party licences — Antresol Server

Antresol Server contains the third-party software below. Each entry gives
the component, its version, its licence and the licence's full text as
shipped with the component.

The list is generated from the build itself: the Go modules in the server
binary's build graph, the Go standard library and runtime, and the npm
packages and vendored code whose source is in its web interface's bundle
(read from that bundle's source maps).

## Summary

| Component | Version | Licence |
|---|---|---|
`
for (const e of all) md += `| [${e.name}](#${anchor(e)}) | ${e.version} | ${e.license} |\n`
const section = (title, list) => {
  md += `\n## ${title}\n`
  for (const e of list) {
    md += `\n<a id="${anchor(e)}"></a>\n### ${e.name} ${e.version}\n\n`
    md += `Licence: ${e.license}  \nSource: ${e.source}\n`
    if (e.repository) md += `Repository: ${e.repository}\n`
    if (e.note) md += `\n${e.note}\n`
    if (e.problem) md += `\n_${e.problem}._\n`
    for (const f of e.files) md += `\n${e.files.length > 1 ? `\`${f.name}\`\n\n` : ''}${fence(f.text)}\n`
  }
}
section('Go: standard library and modules', go)
section('Web interface: npm packages and vendored code', web)
process.stdout.write(md)
for (const p of problems) console.error(`third-party-licenses: ${p.name}: ${p.problem}`)
console.error(`third-party-licenses: ${go.length} Go entries, ${web.length} web entries`)
