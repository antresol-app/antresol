// «Семья» in a reader: the other profiles' places in this book or volume
// (none for a kid), and «Вернуться к …» after a jump to one of them.
import { useFamily } from '../components/FamilyProgress'
import { hasPlace, useReturnPoint } from '../player/familyMarks'
import { useSession } from '../state/session'

export function useReaderFamily<T>(bookId: number) {
  const { user } = useSession()
  const members = useFamily(bookId, !user?.isKid).filter((m) => hasPlace(m, bookId))
  const ret = useReturnPoint<T>()
  return { members, ret }
}
