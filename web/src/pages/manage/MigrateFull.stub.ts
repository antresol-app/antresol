// Stub of the sources build's module of the same name (without .stub), which
// is not in the public tree: imports resolve here only when it is absent
// (tsconfig moduleSuffixes, vite.config.ts resolve.extensions). Same exports
// and types; the core build never reaches them (__LIBRARY_EDITION__).
import type { MigrateFullApi } from './Migrate'

/** Nothing beyond the core's page (the core's subtitle, no extra services). */
export const migrateFull: MigrateFullApi = {
  source: () => undefined,
  TopCards: () => null,
  HistoryAction: () => null,
  ConfirmItems: () => null,
  progressNote: () => '',
  subtitle: 'migrate.subtitle',
}
