// The sections past Главная and the item page are chunks of their own
// (app/routes.tsx): the first page downloads only itself. Shell prefetches
// the sections in the navigation once the browser is idle, so a click on
// one rarely waits.
import { chunk } from './lazyChunk'

export const music = chunk(() => import('../pages/music'))
export const books = chunk(() => import('../pages/books'))
export const browse = chunk(() => import('../pages/libraryPages'))
export const search = chunk(() => import('../pages/Search'))
export const auth = chunk(() => import('../pages/auth'))
export const tv = chunk(() => import('../pages/tv'))

/** Loads the navigation's sections in the background (not on a metered connection that asks to save data). */
export function prefetchSections() {
  const saveData = (navigator as Navigator & { connection?: { saveData?: boolean } }).connection?.saveData
  if (saveData) return
  for (const c of [browse, search, music, books, tv]) void c.load().catch(() => undefined)
}
