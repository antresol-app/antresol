// «Управление» loads as one chunk on first visit (admins only): the library's
// sections, «Просьбы» and «Переезд» (media servers; MigrateFull is the full
// build's part of it, loaded on its own). The sources build's own sections are another chunk
// (./full.ts).
export { Manage } from './Manage'
export { Files } from './Files'
export { Stats } from './Stats'
export { Profiles } from './Profiles'
export { Settings } from './Settings'
export { Requests } from './Requests'
export { Migrate } from './Migrate'
export { Import } from './Import'
