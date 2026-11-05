// The native app's reader bundle: reader-native.html + src/reader/native into
// apple/VaultKit/Sources/VaultReaderWeb/Resources/reader (index.html and
// hashed assets, relative URLs, served by the app's vault-reader:// scheme
// handler). No service worker and no public/ files. The previous output is
// deleted first, except the committed .keep.
import { readdirSync, renameSync, rmSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { defineConfig, type Plugin } from 'vite'

const outDir = resolve(__dirname, '../apple/VaultKit/Sources/VaultReaderWeb/Resources/reader')

/** Deletes the previous bundle (everything but .keep) and names the page index.html. */
function nativeReaderOutput(): Plugin {
  return {
    name: 'vault-native-reader-output',
    apply: 'build',
    buildStart() {
      let names: string[] = []
      try {
        names = readdirSync(outDir)
      } catch {
        return
      }
      for (const name of names) if (name !== '.keep') rmSync(join(outDir, name), { recursive: true, force: true })
    },
    writeBundle() {
      renameSync(join(outDir, 'reader-native.html'), join(outDir, 'index.html'))
    },
  }
}

export default defineConfig({
  base: './',
  publicDir: false,
  plugins: [nativeReaderOutput()],
  build: {
    outDir,
    emptyOutDir: false,
    target: 'es2022',
    assetsInlineLimit: 0,
    modulePreload: { polyfill: false },
    copyPublicDir: false,
    rollupOptions: {
      input: resolve(__dirname, 'reader-native.html'),
    },
  },
})
