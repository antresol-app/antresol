// Stub of the sources build's module of the same name (without .stub), which
// is not in the public tree: imports resolve here only when it is absent
// (tsconfig moduleSuffixes, vite.config.ts resolve.extensions). Same exports
// and types; the core build never reaches them (__LIBRARY_EDITION__).

interface VoiceOverSheetProps {
  itemId: number
  title: string
  onClose: () => void
  container?: Element | null
  pollMs?: number
}

export function VoiceOverSheet(_props: VoiceOverSheetProps) {
  return null
}
