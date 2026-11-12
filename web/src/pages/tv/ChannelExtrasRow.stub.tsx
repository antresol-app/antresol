// Stub of the sources build's module of the same name (without .stub), which
// is not in the public tree: imports resolve here only when it is absent
// (tsconfig moduleSuffixes, vite.config.ts resolve.extensions). Same exports
// and types; the core build has no such row (its server never offers it).

export interface ChannelExtrasRowProps {
  checked: boolean
  onChange: (on: boolean) => void
}

export function ChannelExtrasRow(_props: ChannelExtrasRowProps) {
  return null
}
