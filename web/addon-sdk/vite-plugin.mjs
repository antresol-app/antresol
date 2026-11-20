// The Vite plugin of an Antresol addon's web bundle (contract 1, see
// docs/addons/contract.md). It builds dist/web/index.js, an entry stub that
// imports the hashed chunks/main-<hash>.js, whose
// imports of React, the router and the SDK are not bundled: they read the
// app's own copies from globalThis.__antresol.shared at run time, so an addon
// renders with the app's React and looks like the app.
//
//   import { antresolAddon } from '@antresol/addon-sdk/vite-plugin'
//   export default defineConfig({ plugins: [antresolAddon()] })

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { transform } from 'lightningcss'

/**
 * The shared modules and the names each one has (web/src/addons/shared.ts
 * puts them on globalThis; a test keeps the two lists equal). Importing
 * anything else from these packages is a build error, not a second copy.
 */
export const SHARED = {
  // React's stable public API only: no act, unstable_*, dev-only or
  // server-only names (cache, cacheSignal, captureOwnerStack)
  react: [
    'Activity', 'Children', 'Component', 'Fragment', 'Profiler', 'PureComponent', 'StrictMode', 'Suspense', 'cloneElement',
    'createContext', 'createElement', 'createRef', 'forwardRef', 'isValidElement', 'lazy', 'memo', 'startTransition',
    'use', 'useActionState', 'useCallback', 'useContext', 'useDebugValue', 'useDeferredValue', 'useEffect',
    'useEffectEvent', 'useId', 'useImperativeHandle', 'useInsertionEffect', 'useLayoutEffect', 'useMemo', 'useOptimistic',
    'useReducer', 'useRef', 'useState', 'useSyncExternalStore', 'useTransition', 'version',
  ],
  // production JSX only: jsxDEV (react/jsx-dev-runtime) is not shared
  'react/jsx-runtime': ['Fragment', 'jsx', 'jsxs'],
  'react-dom': ['createPortal'],
  'react-router': ['Link', 'NavLink', 'Navigate', 'Outlet', 'useLocation', 'useNavigate', 'useNavigationType', 'useParams', 'useSearchParams'],
  '@antresol/addon-sdk': [
    'ApiError', 'Chip', 'Chips', 'DownloadButton', 'Icon', 'KIND_ICON', 'MEDIA_SEGMENTS', 'MigratePage', 'PendingOverlay', 'ViewMenu', 'ViewToolbar', 'WatchButton', 'addonApi', 'attachAudioRemote', 'cardHref', 'clock', 'core', 'formatPercent', 'formatRate', 'formatSize', 'kindLabel', 'langBadge', 'langCode', 'languageName', 'locale', 'mediaKindIcon', 'mediaSegmentIcon', 'mediaSegmentOf', 'registerAddon', 'titlePath', 'ui', 'useAddonT', 'useDebounced', 'useEvents', 'useIsCompact', 'useMediaSegments', 'useNowPlaying', 'useUrlView', 'useUser', 'useWatchWay', 'useWatchWays', 'yieldSession',
  ],
}

const PREFIX = '\0antresol-shared:'

/**
 * What the CSS of an addon may not be (contract 1, «Stylesheets»): the app
 * loads index.css beside index.js, so every rule must stay under the
 * addon's own CSS-module classes and leave the app's look alone.
 * The built stylesheet is parsed (lightningcss, Vite's own CSS parser), so
 * strings and comments cannot hide a rule. `id` is the addon's id: the
 * prefix of its @font-face families (and of a @keyframes name that is not
 * hashed). Returns the problems found (empty = fine).
 */
export function cssProblems(css, { id } = {}) {
  const out = []
  const badUrl = (u) => u === '' || /^[a-z][a-z0-9+.-]*:/i.test(u) || u.startsWith('/') || u.startsWith('#') || u.split(/[/\\]/).includes('..')
  const owned = (name) => typeof id === 'string' && id !== '' && name.startsWith(`${id}-`)
  const rule = (r) => {
    const v = r.value
    switch (r.type) {
      case 'media':
      case 'supports':
      case 'container':
      case 'nested-declarations':
        return
      case 'style':
        if (v.declarations?.importantDeclarations?.length > 0) out.push('!important')
        for (const sel of v.selectors) {
          const why = selectorProblem(sel)
          if (why) out.push(`selector "${selectorText(sel)}" ${why}`)
        }
        return
      case 'keyframes': {
        if (v.keyframes?.some((k) => k.declarations?.importantDeclarations?.length > 0)) out.push('!important')
        const name = v.name?.value ?? ''
        // a CSS module's keyframes are hashed (_<local>_<hash>_<line>)
        if (!/^_[\w-]+_[0-9a-z]+_\d+$/i.test(name) && !owned(name)) out.push(`@keyframes ${name}: not a module's (hashed) name, nor prefixed "${id ?? '<id>'}-"`)
        return
      }
      case 'font-face':
        for (const p of v.properties) {
          if (p.type === 'font-family') {
            const name = typeof p.value === 'string' ? p.value : JSON.stringify(p.value)
            if (!owned(name)) out.push(`@font-face family "${name}" is not prefixed "${id ?? '<id>'}-"`)
          } else if (p.type === 'source') {
            for (const s of p.value) if (s.type !== 'url') out.push(`@font-face src ${s.type}(): only a relative url() inside the bundle`)
          } else if (p.type === 'custom') {
            // a descriptor the parser did not understand (a malformed src, for one)
            out.push(`@font-face ${p.value?.name ?? 'descriptor'}: not understood`)
          }
        }
        return
      default:
        out.push(`@${r.type === 'layer-block' || r.type === 'layer-statement' ? 'layer' : r.type} is not allowed`)
    }
  }
  try {
    transform({
      filename: 'index.css',
      code: Buffer.from(css),
      errorRecovery: false,
      visitor: {
        Rule: (r) => void rule(r),
        // the page's frame (room for the floating navigation) is the app's:
        // a page using these tokens draws a second frame inside it
        Variable: (v) => void (FRAME_TOKENS.has(v.name?.ident) && out.push(`var(${v.name.ident}): the page's frame is the app's (an addon page draws none)`)),
        // every URL anywhere in the sheet: url() in any value (typed or a
        // token list, custom properties too), image-set() options, @font-face src
        StyleSheet: (sheet) => void walkUrls(sheet.rules, (u) => badUrl(u.trim()) && out.push(`url(${u})`), (f) => out.push(`${f}() is not allowed`)),
      },
    })
  } catch (e) {
    out.push(`unparsable CSS: ${e.message}`)
  }
  return [...new Set(out)]
}

// the app's page-frame tokens (web/src/design/tokens.css): an addon's page
// sits in the app's frame (pages/AddonPage.tsx), never draws its own
const FRAME_TOKENS = new Set(['--nav-height', '--nav-height-compact', '--page-top'])

// functions whose string arguments are image URLs, and ones refused outright
const URL_FUNCTIONS = new Set(['image-set', 'cross-fade', 'image', 'src', 'url'])
const REFUSED_FUNCTIONS = new Set(['element'])

// walks the parsed sheet: each URL (a { url, loc } node, or a string inside
// one of URL_FUNCTIONS) goes to onUrl; a REFUSED_FUNCTIONS call to onRefused
function walkUrls(node, onUrl, onRefused, inUrlFunction = false) {
  if (Array.isArray(node)) {
    for (const n of node) walkUrls(n, onUrl, onRefused, inUrlFunction)
    return
  }
  if (node === null || typeof node !== 'object') return
  if (typeof node.url === 'string' && 'loc' in node) onUrl(node.url)
  if (inUrlFunction && node.type === 'string' && typeof node.value === 'string') onUrl(node.value)
  let inside = inUrlFunction
  if (node.type === 'function' && typeof node.value?.name === 'string') {
    const name = node.value.name.toLowerCase().replace(/^-[a-z]+-/, '')
    if (REFUSED_FUNCTIONS.has(name)) onRefused(node.value.name)
    if (URL_FUNCTIONS.has(name)) inside = true
  }
  for (const k of Object.keys(node)) if (k !== 'loc') walkUrls(node[k], onUrl, onRefused, inside)
}

const SIBLING = new Set(['next-sibling', 'later-sibling'])

// a selector (lightningcss components): its first compound holds a module
// class (or `&`, the rule it nests in). A compound after `+` or `~` holds
// one too, unless the selector has already gone down (` ` or `>`) from a
// compound with one: a sibling of `.x li` is inside `.x`, a sibling of `.x`
// is the app's
function selectorProblem(sel) {
  let compound = []
  let after = null
  let inside = false
  const ok = () => compound.some((c) => c.type === 'class' || c.type === 'nesting')
  for (const c of [...sel, { type: 'combinator', value: 'end' }]) {
    if (c.type !== 'combinator') {
      compound.push(c)
      continue
    }
    if (after === null && !ok()) return 'is not under a module class'
    if (SIBLING.has(after) && !inside && !ok()) return `reaches a sibling outside the module's classes (after ${after === 'next-sibling' ? '+' : '~'})`
    if ((c.value === 'descendant' || c.value === 'child') && (inside || ok())) inside = true
    after = c.value
    compound = []
  }
  return null
}

function selectorText(sel) {
  const comb = { descendant: ' ', child: ' > ', 'next-sibling': ' + ', 'later-sibling': ' ~ ' }
  return sel
    .map((c) => {
      switch (c.type) {
        case 'class': return `.${c.name}`
        case 'id': return `#${c.name}`
        case 'type': return c.name
        case 'universal': return '*'
        case 'nesting': return '&'
        case 'combinator': return comb[c.value] ?? ` ${c.value} `
        case 'attribute': return `[${c.name}]`
        case 'pseudo-class': return `:${c.kind}`
        case 'pseudo-element': return `::${c.kind}`
        default: return `<${c.type}>`
      }
    })
    .join('')
}
// the packages whose other entry points (react-dom/client, react/jsx-dev-runtime…) are refused
const OWNED = /^(react|react-dom|react-router|react-router-dom|@antresol\/addon-sdk)(\/|$)/

/** The virtual module of shared specifier `s`. */
export function sharedModule(s) {
  const names = SHARED[s]
  const ref = `globalThis.__antresol.shared[${JSON.stringify(s)}]`
  return `const m = ${ref};\nexport default m;\nexport const { ${names.join(', ')} } = m;\n`
}

// the addon's id: from antresol-addon.json in the Vite root or the folder above it
function manifestId(root) {
  for (const dir of [root, path.dirname(root)]) {
    try {
      const id = JSON.parse(readFileSync(path.join(dir, 'antresol-addon.json'), 'utf8')).id
      if (typeof id === 'string' && id !== '') return id
    } catch {
      // no manifest here
    }
  }
  return undefined
}

/**
 * @param {{ entry?: string, outDir?: string, id?: string }} [options]
 *   entry: the bundle's entry module (default src/index.tsx), which calls
 *   registerAddon when it runs; outDir: default dist/web; id: the addon's
 *   id (default: the manifest's, antresol-addon.json in the root or above).
 * @returns {import('vite').Plugin}
 */
export function antresolAddon(options = {}) {
  const entry = options.entry ?? 'src/index.tsx'
  const outDir = options.outDir ?? 'dist/web'
  let id = options.id
  return {
    name: 'antresol-addon',
    enforce: 'pre',
    config() {
      return {
        // chunks and assets load relative to index.js, wherever the app serves it
        base: './',
        oxc: { jsx: { runtime: 'automatic', importSource: 'react', development: false } },
        build: {
          outDir,
          emptyOutDir: true,
          target: 'es2022',
          modulePreload: false,
          // one stylesheet, index.css beside index.js (the app links it)
          cssCodeSplit: false,
          // a file stays a file: never inlined as data: (refused in index.css)
          assetsInlineLimit: 0,
          rolldownOptions: {
            input: entry,
            preserveEntrySignatures: false,
            output: {
              format: 'es',
              // the real entry is a hashed chunk; index.js (generateBundle) only
              // imports it. Only index.js's URL carries the version (?v=), so
              // every other file changes its name with its content, and a lazy
              // chunk that imports code hoisted into the entry imports
              // main-<hash>.js, never index.js again under a second URL
              entryFileNames: 'chunks/main-[hash].js',
              chunkFileNames: 'chunks/[name]-[hash].js',
              assetFileNames: (a) => ((a.names?.[0] ?? a.name ?? '').endsWith('.css') ? 'index.css' : 'assets/[name]-[hash][extname]'),
            },
          },
        },
      }
    },
    configResolved(config) {
      id ??= manifestId(config.root)
    },
    resolveId(source) {
      if (Object.hasOwn(SHARED, source)) return PREFIX + source
      if (OWNED.test(source)) {
        this.error(`antresol-addon: "${source}" is not shared by the app. An addon imports only: ${Object.keys(SHARED).join(', ')} (named exports listed in docs/addons/contract.md).`)
      }
      return null
    },
    load(id) {
      if (id.startsWith(PREFIX)) return sharedModule(id.slice(PREFIX.length))
      return null
    },
    transform(code, id) {
      const file = id.split('?')[0]
      if (!file.endsWith('.css')) return null
      if (!file.endsWith('.module.css')) {
        this.error(`antresol-addon: ${file}: an addon's styles are CSS modules only (X.module.css): a plain stylesheet would restyle the app`)
      }
      if (/:global\b/.test(code)) this.error(`antresol-addon: ${file}: :global() is not allowed (every rule stays under the module's own classes)`)
      if (/@import\b/i.test(code)) this.error(`antresol-addon: ${file}: @import is not allowed`)
      return null
    },
    // after Vite's own CSS step, so index.css is in the bundle to be checked
    generateBundle: {
      order: 'post',
      handler(_, bundle) {
        const entries = Object.values(bundle).filter((f) => f.type === 'chunk' && f.isEntry)
        if (entries.length !== 1) this.error(`antresol-addon: want one entry chunk, got ${entries.length}`)
        if (bundle['index.js']) this.error('antresol-addon: index.js is reserved for the bundle\'s entry stub')
        this.emitFile({ type: 'asset', fileName: 'index.js', source: `import './${entries[0].fileName}'\n` })
        for (const f of Object.values(bundle)) {
          if (f.type !== 'asset' || !f.fileName.endsWith('.css')) continue
          if (f.fileName !== 'index.css') this.error(`antresol-addon: ${f.fileName}: an addon has one stylesheet, index.css`)
          const problems = cssProblems(String(f.source), { id })
          if (problems.length > 0) {
            this.error(`antresol-addon: index.css: ${problems.join('; ')}. An addon's CSS modules style its own classes only, with the app's tokens (docs/addons/contract.md, «Stylesheets»).`)
          }
        }
      },
    },
  }
}

export default antresolAddon
