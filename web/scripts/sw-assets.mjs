// After both builds: prepends to dist/sw.js the files the service worker
// keeps for offline use (from dist/.vite/manifest.json, plus the public
// files) and a hash of those files (names and contents), so every build changes sw.js's bytes and
// browsers pick up the new worker.
import { createHash } from 'node:crypto'
import { readdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { join, relative, sep } from 'node:path'

const dist = new URL('../dist/', import.meta.url).pathname
const manifestPath = join(dist, '.vite', 'manifest.json')
const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))

const files = new Set(['/index.html'])
for (const chunk of Object.values(manifest)) {
  for (const f of [chunk.file, ...(chunk.css ?? []), ...(chunk.assets ?? [])]) if (f) files.add('/' + f)
}
// public/ (icons, web manifest) and anything the manifest leaves out (workers)
const skip = new Set([
  'sw.js',
  '.gitkeep',
  'index.html',
  // the big install icons: read from the network when the app is installed, never offline
  'icons/icon-512.png',
  'icons/icon-maskable-512.png',
])
const walk = (dir) => {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    const rel = relative(dist, p).split(sep).join('/')
    if (rel === '.vite' || skip.has(rel)) continue
    if (statSync(p).isDirectory()) walk(p)
    else files.add('/' + rel)
  }
}
walk(dist)

// Big files few people need are not precached: hls.js (a browser without
// native HLS, player/hlsSupport.ts: Safari and iOS must never download it),
// mpegts.js (a raw MPEG-TS channel, which needs the network anyway) and pdf.js
// with its worker (the PDF reader's own code stays precached; pdf.js is a chunk
// of its own, vite.config.ts, so its name outlives the app's builds). They go in
// a list of their own (self.__VAULT_ON_DEMAND): a browser that needs one fetches
// it on first use, the worker keeps it (shell() in sw.ts), and activate's
// pruning keeps it too while a build still names it (so Chrome does not
// download it again every deploy). A PDF saved for offline reading loads the
// reader's files when it is saved (offline/warm.ts), and the worker loads them
// again (self.__VAULT_PDF_READER, keepPdfReader in sw.ts) when a build names
// others than the ones it kept: a PDF saved before the update still opens offline.
const ON_DEMAND = {
  'hls.js': (src) => /(^|\/)node_modules\/hls\.js\//.test(src),
  'mpegts.js': (src) => /(^|\/)node_modules\/mpegts\.js\//.test(src),
  'pdf.js': (src, chunk) => /(^|\/)node_modules\/pdfjs-dist\//.test(src) || chunk.name === 'pdfjs',
}
const onDemand = new Set()
const pdfReader = []
for (const [what, wanted] of Object.entries(ON_DEMAND)) {
  const found = Object.entries(manifest).filter(([src, chunk]) => wanted(src, chunk))
  if (!found.length) throw new Error(`sw-assets: no ${what} file in the manifest`)
  for (const [, chunk] of found) {
    onDemand.add('/' + chunk.file)
    if (what === 'pdf.js') pdfReader.push('/' + chunk.file)
  }
}
for (const f of onDemand) files.delete(f)

const list = [...files].sort()
// the contents too: a changed public file (icon, web manifest) keeps its name
const hash = createHash('sha1')
for (const f of list) hash.update(f + '\n').update(readFileSync(join(dist, f.slice(1))))
const build = hash.digest('hex').slice(0, 12)
const swPath = join(dist, 'sw.js')
const sw = readFileSync(swPath, 'utf8')
writeFileSync(swPath, `self.__VAULT_ASSETS = ${JSON.stringify(list)}; self.__VAULT_ON_DEMAND = ${JSON.stringify([...onDemand].sort())}; self.__VAULT_PDF_READER = ${JSON.stringify(pdfReader.sort())}; self.__VAULT_BUILD = '${build}';\n${sw}`)
// the manifest is only for this script: not served
rmSync(join(dist, '.vite'), { recursive: true, force: true })
console.log(`sw.js: ${list.length} files, build ${build}`)
