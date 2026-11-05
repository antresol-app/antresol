// A realistic /api/home payload. Artwork is made up (poster, fanart and
// clearlogo shapes by fixtures/make-images.py), stored in fixtures/images and
// served through a mocked /api/images/{name}.
import type { Card, Home } from '../../src/api/types'

let nextId = 100
function c(over: Partial<Card> & Pick<Card, 'title'>): Card {
  return {
    id: nextId++,
    kind: 'movie',
    subtitle: '',
    year: 0,
    poster: '',
    backdrop: '',
    logo: '',
    progress: 0,
    onServer: true,
    ...over,
  }
}

const movie = (key: string, title: string, year: number, subtitle = '') =>
  c({ kind: 'movie', title, year, subtitle, poster: `${key}-poster.jpg`, backdrop: `${key}-backdrop.jpg` })
const series = (key: string, title: string, year: number, subtitle = '') =>
  c({ kind: 'series', title, year, subtitle, poster: `${key}-poster.jpg`, backdrop: `${key}-backdrop.jpg`, logo: `${key}-logo.png` })

export const TEST_MOVIE = 'Интерстеллар'

const interstellar = movie('interstellar', TEST_MOVIE, 2014, 'Фантастика · 2 ч 49 мин')
const wolf = movie('wolf', 'Волк с Уолл-стрит', 2013, 'Драма · 3 ч')
const catchme = movie('catchme', 'Поймай меня, если сможешь', 2002, 'Криминал · 2 ч 21 мин')
const elections = movie('elections', 'День выборов', 2007, 'Комедия · 1 ч 58 мин')
const garlic = movie('garlic', 'Как Витька Чеснок вёз Лёху Штыря в дом инвалидов', 2017, 'Комедия · 1 ч 25 мин')

const pe2 = series('pe2', 'Планета Земля II', 2016, '6 серий · BBC Earth')
const pe3 = series('pe3', 'Планета Земля III', 2023, '8 серий · BBC Earth')
const bp2 = series('bp2', 'Голубая планета II', 2017, '7 серий · BBC Earth')
const seven = series('sevenworlds', 'Семь миров, одна планета', 2019, '7 серий · BBC Earth')
const africa = series('africa', 'Африка', 2013, '6 серий · BBC')
const tiny = series('tiny', 'Крошечный мир', 2020, '2 сезона · Apple TV+')
const drain = series('drain', 'Осушить океан', 2018, 'National Geographic')
const pe1 = series('pe1', 'Планета Земля', 2006, '11 серий · BBC')

const episode = (s: Card, subtitle: string, progress: number) =>
  c({ kind: 'episode', title: s.title, subtitle, poster: s.poster, backdrop: s.backdrop, progress })

export const homeFixture: Home = {
  hero: [
    { ...pe3, subtitle: 'Новый сезон · 8 серий · 4K' },
    { ...interstellar, subtitle: 'Фантастика · 2 ч 49 мин · 4K' },
    { ...seven, subtitle: 'Документальный · 7 серий' },
    { ...wolf, subtitle: 'Драма · 3 ч · 18+' },
    { ...bp2, subtitle: 'Документальный · 7 серий · 4K' },
  ],
  shelves: [
    {
      id: 'continue',
      title: 'Продолжить',
      cards: [
        episode(pe2, 'Сезон 1, серия 3 · Джунгли', 0.6),
        { ...c({ ...interstellar, title: interstellar.title }), subtitle: 'Осталось 1 ч 12 мин', progress: 0.35 },
        episode(seven, 'Серия 2 · Европа', 0.82),
        episode(bp2, 'Серия 4 · Большая синева', 0.15),
        episode(tiny, 'Сезон 2, серия 6 · Город', 0.48),
      ],
    },
    { id: 'recent-movies', title: 'Недавно добавленные фильмы', cards: [interstellar, wolf, catchme, elections, garlic] },
    { id: 'recent-series', title: 'Новые серии', cards: [pe3, seven, bp2, africa, tiny, drain, pe2, pe1] },
    { id: 'watchlist', title: 'Хочу посмотреть', cards: [africa, catchme, drain, pe1, elections] },
  ],
}
