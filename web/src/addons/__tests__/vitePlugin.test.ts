// The addon SDK's Vite plugin (addon-sdk/vite-plugin.mjs): its list of shared
// names is the one shared.ts puts on globalThis, and a bundle built with it
// carries no React of its own, runs on the app's shared modules and
// registers itself; what the app does not share fails the build.
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { build } from 'vite'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { SHARED, antresolAddon, cssProblems } from '../../../addon-sdk/vite-plugin.mjs'
import { contributionsOf, resetAddonsForTests } from '../registry'
import { installShared } from '../shared'

let dir = ''

function project(files: Record<string, string>) {
  for (const [name, text] of Object.entries(files)) {
    mkdirSync(path.dirname(path.join(dir, name)), { recursive: true })
    writeFileSync(path.join(dir, name), text)
  }
}

const bundle = (opts: { id?: string } = {}) => build({ root: dir, configFile: false, logLevel: 'silent', plugins: [antresolAddon(opts)] })

describe('antresolAddon', () => {
  beforeEach(() => {
    // inside the web project: vitest imports the built bundle only from there
    const base = path.join(process.cwd(), 'node_modules', '.cache')
    mkdirSync(base, { recursive: true })
    dir = mkdtempSync(path.join(base, 'addon-plugin-'))
  })
  afterEach(() => {
    rmSync(dir, { recursive: true, force: true })
    resetAddonsForTests()
    globalThis.__antresol = undefined
  })

  it('names exactly what the app shares', () => {
    installShared()
    const shared = globalThis.__antresol!.shared
    expect(Object.keys(shared).sort()).toEqual(Object.keys(SHARED).sort())
    for (const [s, names] of Object.entries(SHARED)) {
      // React's internals (__CLIENT_INTERNALS…) are no addon's business
      const real = Object.keys(shared[s] as object).filter((n) => n !== 'default' && n !== 'module.exports' && !n.startsWith('__'))
      expect([...names].sort(), s).toEqual(real.sort())
    }
  })

  it('shares only React\'s stable public names (contract 1 freezes them)', () => {
    for (const n of ['act', 'cacheSignal', 'captureOwnerStack', 'cache', 'ViewTransition', 'addTransitionType']) expect(SHARED.react, n).not.toContain(n)
    expect(SHARED.react.filter((n) => n.startsWith('unstable_'))).toEqual([])
    for (const n of ['useState', 'useEffect', 'Suspense', 'memo', 'version']) expect(SHARED.react, n).toContain(n)
  })

  it('builds index.js on the shared modules, with hashed chunks, that registers itself', async () => {
    project({
      'src/index.tsx': `
        import { useState } from 'react'
        import { Link } from 'react-router'
        import { registerAddon, ui } from '@antresol/addon-sdk'
        function Page() {
          const [n] = useState(1)
          return <ui.Button><Link to="/">{n}</Link></ui.Button>
        }
        registerAddon('fixture', { routes: [{ path: 'page', title: 'Fixture', element: Page }] })
        addEventListener('later', () => void import('./later'))
      `,
      'src/later.ts': `console.log('later')`,
    })
    await bundle()
    const out = path.join(dir, 'dist', 'web')
    const main = readdirSync(path.join(out, 'chunks')).find((c) => c.startsWith('main-')) ?? ''
    const code = readFileSync(path.join(out, 'chunks', main), 'utf8')
    expect(code).toMatch(/globalThis\.__antresol\.shared\[.react\/jsx-runtime.\]/)
    expect(code).not.toMatch(/react\.production|jsxDEV/)
    expect(readdirSync(path.join(out, 'chunks')).sort()).toEqual([expect.stringMatching(/^later-[\w-]{8,}\.js$/), main])

    installShared()
    await import(/* @vite-ignore */ path.join(out, 'index.js'))
    expect(contributionsOf('fixture')?.routes?.[0]?.title).toBe('Fixture')
  })

  it('index.js is a stub nothing imports: a lazy chunk sharing code with the entry does not run the entry twice', async () => {
    project({
      'src/index.tsx': `
        import { registerAddon } from '@antresol/addon-sdk'
        import { label } from './shared'
        registerAddon('fixture', { strings: { ru: { a: label }, en: { a: label } } })
        addEventListener('later', () => void import('./later'))
      `,
      'src/shared.ts': `export const label = 'общий'`,
      'src/later.ts': `import { label } from './shared'\nconsole.log(label)`,
    })
    await bundle()
    const out = path.join(dir, 'dist', 'web')
    const stub = readFileSync(path.join(out, 'index.js'), 'utf8')
    const main = stub.match(/^import '\.\/(chunks\/main-[\w-]+\.js)'/)
    expect(main, stub).not.toBeNull()
    const chunks = readdirSync(path.join(out, 'chunks'))
    expect(chunks).toContain((main?.[1] ?? '').slice('chunks/'.length))
    for (const c of chunks) expect(readFileSync(path.join(out, 'chunks', c), 'utf8'), c).not.toMatch(/index\.js/)
  })

  it('refuses a module the app does not share', async () => {
    project({ 'src/index.tsx': `import { createRoot } from 'react-dom/client'\nconsole.log(createRoot)` })
    await expect(bundle()).rejects.toThrow(/"react-dom\/client" is not shared/)
  })

  it('builds the CSS modules into one index.css beside index.js, scoped to their classes', async () => {
    project({
      'src/index.tsx': `
        import a from './a.module.css'
        import b from './b.module.css'
        import { registerAddon } from '@antresol/addon-sdk'
        registerAddon('fixture', { strings: { ru: { a: a.box + b.row }, en: { a: '' } } })
      `,
      'src/a.module.css': `.box { color: var(--label); }\n.box svg { fill: currentColor; }\n@media (max-width: 699px) { .box { padding: 0; } }\n@keyframes spin { to { transform: rotate(360deg); } }\n.box { animation: spin 1s; }`,
      'src/b.module.css': `.row:hover { background: var(--fill-1); }`,
    })
    await bundle()
    const out = path.join(dir, 'dist', 'web')
    expect(readdirSync(out).sort()).toEqual(['chunks', 'index.css', 'index.js'])
    const css = readFileSync(path.join(out, 'index.css'), 'utf8')
    expect(css).toMatch(/var\(--label\)/)
    expect(cssProblems(css)).toEqual([])
  })

  it.each([
    ['a plain stylesheet', { 'src/look.css': 'p { color: red }' }, `import './look.css'`, /CSS modules only/],
    ['a global selector', { 'src/a.module.css': ':global(body) { margin: 0 }' }, `import a from './a.module.css'\nconsole.log(a)`, /:global\(\) is not allowed/],
    ['@import', { 'src/a.module.css': '@import "./b.css";\n.x { color: red }' }, `import a from './a.module.css'\nconsole.log(a)`, /@import is not allowed/],
    [':root', { 'src/a.module.css': ':root { --a: 1 }' }, `import a from './a.module.css'\nconsole.log(a)`, /":root" is not under a module class/],
    ['a bare element', { 'src/a.module.css': 'body { margin: 0 }' }, `import a from './a.module.css'\nconsole.log(a)`, /"body" is not under a module class/],
    ['*', { 'src/a.module.css': '* { margin: 0 }' }, `import a from './a.module.css'\nconsole.log(a)`, /"\*" is not under a module class/],
    ['an attribute', { 'src/a.module.css': '[hidden] { display: none }' }, `import a from './a.module.css'\nconsole.log(a)`, /not under a module class/],
    ['an id', { 'src/a.module.css': '#app { margin: 0 }' }, `import a from './a.module.css'\nconsole.log(a)`, /not under a module class/],
    ['!important', { 'src/a.module.css': '.x { color: red !important }' }, `import a from './a.module.css'\nconsole.log(a)`, /!important/],
    ['an outside url()', { 'src/a.module.css': '.x { background: url(https://elsewhere.example/a.png) }' }, `import a from './a.module.css'\nconsole.log(a)`, /url\(https:/],
    // a hand-rolled scanner was fooled by these two (review I-1)
    ['a rule hidden by /* */ inside strings', { 'src/a.module.css': '.x{content:"/*"} body{color:red} .y{content:"*/"}' }, `import a from './a.module.css'\nconsole.log(a)`, /"body" is not under a module class/],
    ['a rule hidden by { in a @font-face string', { 'src/a.module.css': '@font-face{font-family:"{";src:local(Arial)} html{color:blue}' }, `import a from './a.module.css'\nconsole.log(a)`, /"html" is not under a module class/],
    ['@property', { 'src/a.module.css': '@property --label{syntax:"<length>";inherits:false;initial-value:0}\n.x{color:red}' }, `import a from './a.module.css'\nconsole.log(a)`, /@property is not allowed/],
    ['a later sibling', { 'src/a.module.css': '.x ~ * { display: none }' }, `import a from './a.module.css'\nconsole.log(a)`, /reaches a sibling outside the module's classes \(after ~\)/],
    ['a next sibling', { 'src/a.module.css': '.x + div { display: none }' }, `import a from './a.module.css'\nconsole.log(a)`, /reaches a sibling outside the module's classes \(after \+\)/],
    ['a list whose second selector is global', { 'src/a.module.css': '.x, body { margin: 0 }' }, `import a from './a.module.css'\nconsole.log(a)`, /"body" is not under a module class/],
    ['a @font-face family not prefixed with the id', { 'src/a.module.css': '@font-face{font-family:"Inter";src:url(./f.woff2)}\n.x{font-family:Inter}', 'src/f.woff2': 'x' }, `import a from './a.module.css'\nconsole.log(a)`, /@font-face family "Inter" is not prefixed "fixture-"/],
  ])('refuses %s', async (_name, files, imp, msg) => {
    project({ 'src/index.tsx': imp, ...files })
    await expect(bundle({ id: 'fixture' })).rejects.toThrow(msg)
  })

  // URLs inside image-set() escaped the url() check (re-review 1)
  it.each([
    ['background-image:image-set("https://e.example/a.png" 1x)', /url\(https:\/\/e\.example\/a\.png\)/],
    ['background-image:-webkit-image-set(url(https://e.example/a.png) 1x)', /url\(https:\/\/e\.example\/a\.png\)/],
    ['mask-image:image-set("//e.example/a.png" 1x)', /url\(\/\/e\.example\/a\.png\)/],
    ['cursor:image-set("https://e.example/a.png" 1x),auto', /url\(https:/],
    ['content:image-set("https://e.example/a.png")', /url\(https:/],
    ['list-style-image:image-set("https://e.example/a.png" 1x)', /url\(https:/],
    ['--i:image-set("https://e.example/a.png")}\n.y{background-image:var(--i)', /url\(https:/],
  ])('refuses an outside URL in %s', async (decl, msg) => {
    project({ 'src/index.tsx': `import a from './a.module.css'\nconsole.log(a)`, 'src/a.module.css': `.x{${decl}}` })
    await expect(bundle({ id: 'fixture' })).rejects.toThrow(msg)
  })

  it('keeps a small relative file a file, and allows the addon\'s own font', async () => {
    // a 1x1 PNG: under Vite's 4 KiB default it would be inlined as data:
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=', 'base64')
    project({
      'src/index.tsx': `import a from './a.module.css'\nconsole.log(a)`,
      'src/a.module.css': `@font-face { font-family: "fixture-mono"; src: url(./mono.woff2) format("woff2"); }\n.u { background: url(./tiny.png); font-family: "fixture-mono"; }`,
      'src/mono.woff2': 'font',
    })
    writeFileSync(path.join(dir, 'src', 'tiny.png'), png)
    await bundle({ id: 'fixture' })
    const out = path.join(dir, 'dist', 'web')
    const css = readFileSync(path.join(out, 'index.css'), 'utf8')
    expect(css).not.toMatch(/data:/)
    const tiny = readdirSync(path.join(out, 'assets')).find((f) => f.startsWith('tiny-'))
    expect(tiny).toBeDefined()
    expect(css).toContain(`assets/${tiny}`)
    expect(cssProblems(css, { id: 'fixture' })).toEqual([])
  })

  it('takes the id from antresol-addon.json above the web project', async () => {
    project({
      'antresol-addon.json': JSON.stringify({ id: 'fixture' }),
      'web/src/index.tsx': `import a from './a.module.css'\nconsole.log(a)`,
      'web/src/a.module.css': `@font-face { font-family: "fixture-mono"; src: url(./mono.woff2); }\n.u { font-family: "fixture-mono"; }`,
      'web/src/mono.woff2': 'font',
    })
    await build({ root: path.join(dir, 'web'), configFile: false, logLevel: 'silent', plugins: [antresolAddon()] })
    expect(readdirSync(path.join(dir, 'web', 'dist', 'web'))).toContain('index.css')
  })

  it('cssProblems names what a built stylesheet may not hold', () => {
    expect(cssProblems('._a_1 svg{x:1} ._a_1 > div{x:1} @media (x){._b_2{y:1}} @supports (x:1){._b_2{y:1}} @container (min-width:1px){._b_2{y:1}} @keyframes _spin_1fq2l_9{to{z:1}} ._c{background:url(./assets/a.png)} ._d{&:hover{x:1} ._e &{x:1}} ._f ~ ._g{x:1} ._h + ._i:hover{x:1} ._j li + li{x:1} ._k > span ~ :not(b){x:1}')).toEqual([])
    // a sibling inside an outer module class stays inside; one of the outermost anchor does not
    expect(cssProblems('._a ._b ~ *{x:1}')).toEqual([])
    expect(cssProblems('._a + li ~ *{x:1}')).toEqual(['selector "._a + li ~ *" reaches a sibling outside the module\'s classes (after +)'])
    expect(cssProblems('html{x:1}')).toEqual(['selector "html" is not under a module class'])
    expect(cssProblems('._a{background:url(/x.png)} ._b{background:url(../x.png)} ._c{background:url(data:image/png;base64,AA)}')).toHaveLength(3)
    expect(cssProblems('._a{x:1 !important}')).toEqual(['!important'])
    expect(cssProblems('@keyframes spin{to{z:1}}')).toEqual(['@keyframes spin: not a module\'s (hashed) name, nor prefixed "<id>-"'])
    expect(cssProblems('@keyframes fixture-spin{to{z:1}}', { id: 'fixture' })).toEqual([])
    expect(cssProblems('@font-face{font-family:"fixture-a";src:local(Arial)}', { id: 'fixture' })).toEqual(['@font-face src local(): only a relative url() inside the bundle'])
    expect(cssProblems('@font-face{font-family:"fixture-a";src:url(https://e.example/a.woff2)}', { id: 'fixture' })).toEqual(['url(https://e.example/a.woff2)'])
    // every URL of the sheet, wherever it sits
    expect(cssProblems('._x{background-image:image-set("../a.png" 1x)}')).toEqual(['url(../a.png)'])
    expect(cssProblems('._x{--i:image-set("../a.png")}')).toEqual(['url(../a.png)'])
    expect(cssProblems('._x{background-image:image-set("./a.png" 1x, url(./b.png) 2x)}')).toEqual([])
    expect(cssProblems('._x{content:"https://only-text.example"}')).toEqual([])
    expect(cssProblems('._x{background:-moz-element(#app)}')).toEqual(['-moz-element() is not allowed'])
    // a src the parser could not read, and !important in a keyframe
    expect(cssProblems('@font-face{font-family:"fixture-a";src:local(Arial) xyz}', { id: 'fixture' })).toEqual(['@font-face src: not understood'])
    expect(cssProblems('@keyframes _k_a1b2c_1{to{color:red !important}}')).toEqual(['!important'])
    for (const [css, why] of ([
      ['@import "x.css";', '@import'],
      ['@namespace svg url(http://www.w3.org/2000/svg);', '@namespace'],
      ['@page{margin:0}', '@page'],
      ['@layer a{._a{x:1}}', '@layer'],
      ['@layer a;', '@layer'],
      ['@counter-style c{system:cyclic;symbols:"*"}', '@counter-style'],
      ['@font-feature-values Font{@swash{a:1}}', '@font-feature-values'],
    ] as const)) expect(cssProblems(css).join('; '), css).toMatch(new RegExp(`(^|; )${why} is not allowed`))
  })
})
