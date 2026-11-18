// Stub of the sources build's module of the same name (without .stub), which
// is not in the public tree: imports resolve here only when it is absent
// (tsconfig moduleSuffixes, vite.config.ts resolve.extensions). Same exports
// and types; the core build never reaches them (__LIBRARY_EDITION__).
// The sources build's strings: none. Only the names of its keys that core
// code names (in branches the core build never runs), so they are
// keys of the catalog type; the text stays in the sources build.
type Named =
  | 'import.preview.hint'
  | 'import.state.noFormat'
  | 'import.state.searching'
  | 'music.pending.searching'

export default {} as Record<Named, string>
