// Stub of the sources build's module of the same name (without .stub), which
// is not in the public tree: imports resolve here only when it is absent
// (tsconfig moduleSuffixes, vite.config.ts resolve.extensions). Same exports
// and types; the core build never reaches them (__LIBRARY_EDITION__).
import type { PendingState } from '../api/types'
import type { TFunction } from '../i18n'

export function pendingLabel(_t: TFunction, _p: PendingState): string {
  return ''
}

export function PendingOverlay(_props: { pending: PendingState; corner?: boolean }) {
  return null
}
