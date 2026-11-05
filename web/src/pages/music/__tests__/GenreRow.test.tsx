import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { Genre, MusicHome as MusicHomeData } from '../../../api/types'
import { audio, resetAudioForTests } from '../../../player/audio/engine'
import { GenreRow } from '../GenreRow'
import { MusicHome } from '../MusicHome'
import { json, song } from './fixtures'
import { setLanguagePref } from '../../../i18n'

const NAMES = ['Рок', 'Поп', 'Джаз', 'Блюз', 'Метал', 'Панк', 'Фолк', 'Электроника', 'Хип-хоп', 'Классика']
const genres: Genre[] = Array.from({ length: 20 }, (_, i) => ({ name: i < 10 ? NAMES[i]! : `Жанр ${i}`, albums: 1, songs: 20 - i }))

const home: MusicHomeData = {
  shelves: [
    {
      id: 'recent-albums',
      title: 'Недавно добавленные',
      cards: [
        {
          id: 1,
          kind: 'album',
          title: 'Первый альбом',
          subtitle: 'Тестовый артист',
          poster: '',
          backdrop: '',
          logo: '',
          progress: 0,
          onServer: true,
        },
      ],
    },
  ],
}

let urls: string[]
let shuffleResponse: () => Response
let genresResponse: () => Response

beforeEach(() => {
  resetAudioForTests()
  urls = []
  shuffleResponse = () => json({ tracks: [song(1), song(2)] })
  genresResponse = () => json({ genres })
  vi.spyOn(HTMLMediaElement.prototype, 'play').mockImplementation(() => Promise.resolve())
  vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(() => {})
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      urls.push(url)
      if (url === '/api/music/home') return json(home)
      if (url === '/api/music/genres') return genresResponse()
      if (url.startsWith('/api/music/shuffle')) return shuffleResponse()
      return json({}, 404)
    }),
  )
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  resetAudioForTests()
})

describe('GenreRow', () => {
  it('renders every genre as a chip', () => {
    render(<GenreRow genres={genres} />)
    expect(screen.getByRole('heading', { name: 'Жанры' })).toBeInTheDocument()
    const chips = screen.getAllByRole('button')
    expect(chips).toHaveLength(20)
    expect(chips[0]).toHaveAccessibleName('Перемешать «Рок»')
    expect(chips[0]).toHaveTextContent('Рок')
  })

  it('speaks English when the interface is English', () => {
    setLanguagePref('en')
    render(<GenreRow genres={genres} />)
    expect(screen.getByRole('heading', { name: 'Genres' })).toBeInTheDocument()
    expect(screen.getAllByRole('button')[0]).toHaveAccessibleName('Shuffle “Рок”')
  })

  it('a chip shuffles its genre', async () => {
    render(<GenreRow genres={genres} />)
    fireEvent.click(screen.getByRole('button', { name: 'Перемешать «Поп»' }))
    await waitFor(() => expect(audio.get().queue).toHaveLength(2))
    expect(urls).toContain(`/api/music/shuffle?source=genre&genre=${encodeURIComponent('Поп')}`)
  })

  it('an empty list renders nothing', () => {
    const { container } = render(<GenreRow genres={[]} />)
    expect(container).toBeEmptyDOMElement()
  })
})

function renderHome() {
  return render(
    <MemoryRouter>
      <MusicHome />
    </MemoryRouter>,
  )
}

describe('MusicHome', () => {
  it('«Перемешать все» shuffles the whole library', async () => {
    renderHome()
    fireEvent.click(await screen.findByRole('button', { name: 'Перемешать все' }))
    await waitFor(() => expect(audio.get().queue).toHaveLength(2))
    expect(urls).toContain('/api/music/shuffle?source=all')
    expect(screen.getByRole('heading', { name: 'Жанры' })).toBeInTheDocument()
  })

  it('in English: title, links and «Shuffle All»', async () => {
    setLanguagePref('en')
    renderHome()
    expect(await screen.findByRole('button', { name: 'Shuffle All' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: 'Music' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'All Albums' })).toBeInTheDocument()
  })

  it('a failed shuffle shows an alert under the header', async () => {
    shuffleResponse = () => json({ error: 'Не найдено' }, 404)
    renderHome()
    fireEvent.click(await screen.findByRole('button', { name: 'Перемешать все' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Не найдено')
  })

  it('hides the genre row when genres fail to load', async () => {
    genresResponse = () => json({ error: 'Ошибка' }, 500)
    renderHome()
    expect(await screen.findByRole('button', { name: 'Перемешать все' })).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Жанры' })).not.toBeInTheDocument()
  })
})
