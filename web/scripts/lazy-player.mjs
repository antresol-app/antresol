// The video player stays out of the first load: after `vite build`, the
// entry chunk (index.html) and every chunk it imports statically must not
// hold src/player/VideoPlayer.tsx; the player is reached by a dynamic
// import only (the core's player pages, the addon SDK's ui.Player). Reads
// dist/.vite/manifest.json (build.manifest); run by `npm run build` before
// sw-assets removes the manifest. Exits 1 on a breach.
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

export const PLAYER = 'src/player/VideoPlayer.tsx'

/** The manifest keys the entry loads before it runs: itself and its static imports, transitively. */
export function staticClosure(manifest, entry) {
  const seen = new Set()
  const stack = [entry]
  while (stack.length > 0) {
    const k = stack.pop()
    if (seen.has(k) || !manifest[k]) continue
    seen.add(k)
    for (const i of manifest[k].imports ?? []) stack.push(i)
  }
  return seen
}

/**
 * Whether a manifest chunk is the player's: its facade module (the key
 * src/player/VideoPlayer.tsx) or a shared chunk named after it
 * (_VideoPlayer-<hash>.js, the player imported by several pages).
 */
export function isPlayerChunk(key, chunk) {
  return key === PLAYER || (/\.js$/.test(chunk.file) && /(^|\/)VideoPlayer-[^/]*\.js$/.test(chunk.file))
}

/**
 * What is wrong (empty: nothing): the entry is missing, a chunk of its
 * static closure is the player's, or no chunk is (the player was merged
 * into another chunk, maybe the entry's).
 */
export function lazyPlayerProblems(manifest) {
  const entry = Object.keys(manifest).find((k) => manifest[k].isEntry && k.endsWith('index.html'))
  if (!entry) return ['no index.html entry in the manifest']
  const problems = []
  const closure = staticClosure(manifest, entry)
  for (const k of closure) if (isPlayerChunk(k, manifest[k])) problems.push(`${k} (the video player) is in the first load, statically imported from ${entry}`)
  const own = Object.keys(manifest).filter((k) => isPlayerChunk(k, manifest[k]))
  if (own.length === 0) problems.push(`${PLAYER} has no chunk of its own: it was merged into another one`)
  return problems
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  const dist = process.argv[2] ?? join(fileURLToPath(new URL('..', import.meta.url)), 'dist')
  const manifest = JSON.parse(readFileSync(join(dist, '.vite', 'manifest.json'), 'utf8'))
  const problems = lazyPlayerProblems(manifest)
  if (problems.length > 0) {
    for (const p of problems) console.error(`lazy-player: ${p}`)
    process.exit(1)
  }
  console.log(`lazy-player: ${PLAYER} loads on demand only`)
}
