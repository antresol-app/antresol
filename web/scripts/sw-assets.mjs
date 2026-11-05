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
const skip = new Set(['sw.js', '.gitkeep', 'index.html'])
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

const list = [...files].sort()
// the contents too: a changed public file (icon, web manifest) keeps its name
const hash = createHash('sha1')
for (const f of list) hash.update(f + '\n').update(readFileSync(join(dist, f.slice(1))))
const build = hash.digest('hex').slice(0, 12)
const swPath = join(dist, 'sw.js')
const sw = readFileSync(swPath, 'utf8')
writeFileSync(swPath, `self.__VAULT_ASSETS = ${JSON.stringify(list)}; self.__VAULT_BUILD = '${build}';\n${sw}`)
// the manifest is only for this script: not served
rmSync(join(dist, '.vite'), { recursive: true, force: true })
console.log(`sw.js: ${list.length} files, build ${build}`)
