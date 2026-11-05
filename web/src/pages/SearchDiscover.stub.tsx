// Stub of the sources build's module of the same name (without .stub), which
// is not in the public tree: imports resolve here only when it is absent
// (tsconfig moduleSuffixes, vite.config.ts resolve.extensions). Same exports
// and types; the core build never reaches them (__LIBRARY_EDITION__).
import type { SearchScope } from '../app/sections'

export function FindAndAdd(_props: { text: string; q: string; scope: SearchScope; nothing: boolean; admin: boolean; kid?: boolean }) {
  return null
}
