import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ChannelEditor } from '../ChannelEditor'
import type { Channel, ChannelRules } from '../../../api/channels'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })

function server() {
  const sent: { method: string; body: { number?: number; rules: ChannelRules } }[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/api/channels/options')) return json({ genres: [], decades: [], titles: [], profiles: [], extrasAvailable: false })
      if (url.endsWith('/api/channels/preview')) return json({ movies: 0, series: 0, episodes: 0, items: [] })
      if (init?.method === 'POST' || init?.method === 'PUT') {
        sent.push({ method: init.method, body: JSON.parse(String(init.body)) })
        return json({ id: 7, number: 7, name: 'x', category: '', personal: false, auto: false, enabled: true, canEdit: true, logo: 0 })
      }
      return json({ error: 'unexpected ' + url }, 500)
    }),
  )
  return sent
}

afterEach(() => vi.unstubAllGlobals())

// «Все сериалы» as the generator makes it beside «Мультсериалы»
const generated = {
  id: 7,
  number: 7,
  name: 'Все сериалы',
  category: 'Кино и сериалы',
  personal: false,
  auto: true,
  enabled: true,
  canEdit: true,
  logo: 0,
  rules: { kinds: ['series'], notGenres: ['мультфильм'], audioLang: 'ru', order: 'shuffle', skipSegments: true },
} as unknown as Channel

describe('ChannelEditor: rules it has no field for', () => {
  it('sends notGenres and audioLang back as they came when a generated channel is renamed', async () => {
    const sent = server()
    render(<ChannelEditor channel={generated} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />)
    const name = await screen.findByLabelText('Название')
    await userEvent.clear(name)
    await userEvent.type(name, 'Сериалы')
    await userEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(sent).toHaveLength(1))
    expect(sent[0]?.method).toBe('PUT')
    expect(sent[0]?.body.rules).toMatchObject({ kinds: ['series'], notGenres: ['мультфильм'], audioLang: 'ru', order: 'shuffle', skipSegments: true })
  })

  it('keeps them for a channel that plays its own list, and drops the tag rules it ignores', async () => {
    const sent = server()
    const ch = { ...generated, auto: false, rules: { ...generated.rules, genres: ['комедия'], picks: [{ kind: 'title', id: 4 }] } } as unknown as Channel
    render(<ChannelEditor channel={ch} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />)
    await screen.findByLabelText('Название')
    await userEvent.click(screen.getByRole('button', { name: 'Сохранить' }))
    await waitFor(() => expect(sent).toHaveLength(1))
    const r = sent[0]!.body.rules
    expect(r.picks).toEqual([{ kind: 'title', id: 4 }])
    expect(r.audioLang).toBe('ru')
    expect(r.kinds).toBeUndefined()
    expect(r.genres).toBeUndefined()
  })
})

describe('ChannelEditor: the number', () => {
  it('is an administrator’s field: a profile editing its own channel has none', async () => {
    server()
    const own = { ...generated, auto: false, personal: true } as unknown as Channel
    const { unmount } = render(<ChannelEditor channel={own} me={{ id: 2, isAdmin: false }} onClose={() => {}} onSaved={() => {}} />)
    await screen.findByLabelText('Название')
    expect(screen.queryByLabelText('Номер')).toBeNull()
    unmount()
    render(<ChannelEditor channel={generated} me={{ id: 1, isAdmin: true }} onClose={() => {}} onSaved={() => {}} />)
    expect(await screen.findByLabelText('Номер')).toBeInTheDocument()
  })
})
