// The service worker: a second build of src/offline/sw.ts into dist/sw.js
// (one classic script, no chunks). scripts/sw-assets.mjs then prepends the
// list of built files and the build hash.
import { existsSync } from 'node:fs'
import { defineConfig } from 'vite'

export default defineConfig({
  // the edition as in vite.config.ts: the tree's (no src/app/routesFull.tsx: library)
  define: { __LIBRARY_EDITION__: JSON.stringify(!existsSync(new URL('src/app/routesFull.tsx', import.meta.url))) },
  publicDir: false,
  build: {
    outDir: 'dist',
    emptyOutDir: false,
    target: 'es2022',
    copyPublicDir: false,
    lib: {
      entry: 'src/offline/sw.ts',
      formats: ['iife'],
      name: 'vaultSW',
      fileName: () => 'sw.js',
    },
  },
})
