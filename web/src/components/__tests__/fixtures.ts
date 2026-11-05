import type { Card, User } from '../../api/types'

export const admin: User = {
  id: 1,
  name: 'Александр',
  avatar: 'grad-1',
  hasPassword: true,
  isAdmin: true,
  isKid: false,
  maxRating: null,
}

export function card(i: number, over: Partial<Card> = {}): Card {
  return {
    id: i,
    kind: 'movie',
    title: `Фильм ${i}`,
    subtitle: '',
    year: 2000 + i,
    poster: `p${i}.jpg`,
    backdrop: `b${i}.jpg`,
    logo: '',
    progress: 0,
    onServer: true,
    ...over,
  }
}
