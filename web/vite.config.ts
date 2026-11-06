/// <reference types="vitest/config" />
import { existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'

const backend = process.env.VAULT_DEV_BACKEND ?? 'http://localhost:8420'

// The edition is the tree's, not a setting: the sources build's modules live
// beside their stubs (X.stub.ts / X.stub.tsx, picked by resolve.extensions
// below only when X itself is absent), and a tree without
// src/app/routesFull.tsx (the public one) builds the core build:
// __LIBRARY_EDITION__ is the literal true, so the screens, routes and
// strings of the sources build's other features are dead code and left out of
// the bundle (docs/build-tags.md).
const libraryEdition = !existsSync(new URL('src/app/routesFull.tsx', import.meta.url))

// The boot script (src/boot): an input of its own, so it stays a tiny chunk
// with no imports, put first in <head> as an async module. It starts GET
// /api/me while the app's bundle downloads (Vite would fold a second
// <script> of index.html into the app's entry chunk, which runs last).
const BOOT = 'src/boot/index.ts'
function bootScript(): Plugin {
  return {
    name: 'vault-boot-script',
    transformIndexHtml: {
      order: 'post',
      handler(html, ctx) {
        const chunk = ctx.bundle && Object.values(ctx.bundle).find((c) => c.type === 'chunk' && c.isEntry && c.name === 'boot')
        if (ctx.bundle && !chunk) throw new Error('vault-boot-script: no boot chunk in the bundle')
        const src = chunk ? `/${chunk.fileName}` : `/${BOOT}`
        // the catalogs' chunks (i18n/core.ts loads them on demand): the boot
        // script preloads the active one beside the bundle (boot/early.ts)
        const catalog = (lang: string) =>
          ctx.bundle && Object.values(ctx.bundle).find((c) => c.type === 'chunk' && c.facadeModuleId?.endsWith(`/src/i18n/${lang}.ts`))
        const ru = catalog('ru')
        const en = catalog('en')
        if (ctx.bundle && (!ru || !en)) throw new Error('vault-boot-script: no catalog chunks in the bundle')
        const i18n = ru && en ? ` data-i18n-ru="/${ru.fileName}" data-i18n-en="/${en.fileName}"` : ''
        // right after the charset and viewport metas: the first request after the page's own
        const at = /<meta name="viewport"[^>]*>/
        if (!at.test(html)) throw new Error('vault-boot-script: no viewport meta in index.html')
        return html.replace(at, (m) => `${m}\n    <script type="module" async crossorigin src="${src}"${i18n}></script>`)
      },
    },
  }
}

export default defineConfig({
  plugins: [react(), bootScript()],
  define: { __LIBRARY_EDITION__: JSON.stringify(libraryEdition) },
  // Vite's defaults (this list replaces them), then the stubs: `./X` is X.ts
  // or X.tsx when it exists, else X.stub.ts / X.stub.tsx (tsconfig's
  // moduleSuffixes does the same for tsc).
  resolve: { extensions: ['.mjs', '.js', '.mts', '.ts', '.jsx', '.tsx', '.json', '.stub.ts', '.stub.tsx'] },
  server: {
    proxy: {
      '/api': { target: backend, changeOrigin: false },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'es2022',
    assetsInlineLimit: 0,
    // scripts/sw-assets.mjs reads it for the service worker's file list
    manifest: true,
    rolldownOptions: {
      input: {
        index: fileURLToPath(new URL('index.html', import.meta.url)),
        boot: fileURLToPath(new URL(BOOT, import.meta.url)),
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: false,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/__tests__/**/*.test.{ts,tsx}'],
    css: { modules: { classNameStrategy: 'non-scoped' } },
  },
})
