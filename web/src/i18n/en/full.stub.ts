// Stub of the sources build's module of the same name (without .stub), which
// is not in the public tree: imports resolve here only when it is absent
// (tsconfig moduleSuffixes, vite.config.ts resolve.extensions). Same exports
// and types; the core build never reaches them (__LIBRARY_EDITION__).
// The sources build's strings: none. Only the names of its keys that core
// code names (in branches the core build never runs), so they are
// keys of the catalog type; the text stays in the sources build.
type Named =
  | 'books.empty.find'
  | 'books.empty.hintFull'
  | 'browse.emptyAdminFull'
  | 'browse.find'
  | 'browse.home.emptyAdminFull'
  | 'browse.home.findMovie'
  | 'common.download.seeding'
  | 'common.watchOnline'
  | 'comp.disk.downloads'
  | 'files.info.seeding'
  | 'files.info.torrent'
  | 'files.kind.torrent'
  | 'files.op.seedWarning'
  | 'files.op.stopSeeding'
  | 'import.preview.hint'
  | 'import.state.noFormat'
  | 'import.state.searching'
  | 'mfiles.seedingText'
  | 'mfiles.seedingTitle'
  | 'migrate.report.rollback'
  | 'music.emptyHintFull'
  | 'music.find'
  | 'music.pending.searching'
  | 'player.video.stalledError'
  | 'profiles.kidHint'
  | 'settings.incoming.blocked'
  | 'settings.kindLangs.hint'
  | 'settings.maxSize.hint'
  | 'settings.musicFormat.hint'
  | 'settings.online.unreachable'
  | 'settings.seedingGroup'
  | 'settings.studios.emptyFull'
  | 'settings.studios.hintFull'
  | 'settings.uploadLimit'
  | 'settings.uploadLimit.hint'
  | 'stats.disk.hint.downloads'
  | 'stats.subtitle'
  | 'stats.tile.seeding'

export default {} as Record<Named, string>
