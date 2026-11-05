import { useSession } from './session'

/**
 * Whether this server fetches what a library lacks (the sources build, not on a
 * library-only server). The playlist import words its texts and offers
 * «Повторить» by it: without downloads the albums it has are linked and the
 * rest are «Нет в медиатеке».
 */
export function useCanDownload(): boolean {
  const { user } = useSession()
  return !__LIBRARY_EDITION__ && !user?.libraryOnly
}
