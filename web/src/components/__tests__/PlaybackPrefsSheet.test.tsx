import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PlaybackPrefsSheet } from '../PlaybackPrefsSheet'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const household = { audioLangs: ['ru', 'en'], subMode: 'off', subLangs: ['ru', 'en'], subSize: 'm', subBackground: 'translucent', custom: false }

type Call = { url: string; method: string; body?: unknown }
let calls: Call[]

beforeEach(() => {
  calls = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      const method = init?.method ?? 'GET'
      const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
      calls.push({ url, method, body })
      if (method === 'PUT' && url.endsWith('/prefer-cleaned')) return json({ ...household, preferCleaned: body.on })
      if (method === 'PUT') return json({ ...body, custom: true })
      if (method === 'DELETE') return json(household)
      return json(household)
    }),
  )
})
afterEach(() => vi.unstubAllGlobals())

describe('PlaybackPrefsSheet', () => {
  it('shows the household defaults, saves the profile’s own', async () => {
    const onClose = vi.fn()
    render(<PlaybackPrefsSheet onClose={onClose} />)
    expect(await screen.findByText('Сейчас — языки семьи из настроек.')).toBeInTheDocument()
    // English first: moved up by its handle
    expect(within(screen.getByRole('list', { name: 'Язык звука' })).getAllByRole('listitem').map((li) => li.dataset.value)).toEqual(['ru', 'en'])
    screen.getByRole('button', { name: 'Переместить: Английский' }).focus()
    await userEvent.keyboard('{ArrowUp}')
    // the app's pop-up, not a native select
    expect(screen.queryByRole('combobox')).toBeNull()
    await userEvent.click(screen.getByRole('button', { name: 'Субтитры: Выключены' }))
    await userEvent.click(screen.getByRole('menuitemradio', { name: 'Если звук не на моём языке' }))
    expect(screen.getByRole('list', { name: 'Язык субтитров' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('radio', { name: 'Очень крупный' }))
    await userEvent.click(screen.getByRole('radio', { name: 'Сплошной' }))
    // saved by itself; «Готово» sends what still waits and closes
    await userEvent.click(screen.getByRole('button', { name: 'Готово' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(screen.queryByRole('button', { name: 'Сохранить' })).toBeNull()
    const put = calls.filter((c) => c.method === 'PUT').at(-1)!
    expect(put.url).toBe('/api/me/playback-prefs')
    expect(put.body).toEqual({ audioLangs: ['en', 'ru'], subMode: 'foreign', subLangs: ['ru', 'en'], subSize: 'xl', subBackground: 'solid' })
  })

  it('«Выбирать очищенную озвучку»: off by default, explained, saved as it flips, apart from the defaults', async () => {
    const onClose = vi.fn()
    render(<PlaybackPrefsSheet onClose={onClose} />)
    const sw = await screen.findByRole('switch', { name: 'Выбирать очищенную озвучку' })
    expect(sw).toHaveAttribute('aria-checked', 'false')
    expect(sw).toHaveAccessibleDescription('Без оригинальной речи под закадровым переводом')
    await userEvent.click(sw)
    expect(sw).toHaveAttribute('aria-checked', 'true')
    await waitFor(() => expect(calls.some((c) => c.method === 'PUT')).toBe(true))
    expect(calls.filter((c) => c.method === 'PUT')).toEqual([{ url: '/api/me/playback-prefs/prefer-cleaned', method: 'PUT', body: { on: true } }])
    // the household's languages stay followed: closing saves nothing else
    expect(screen.getByText('Сейчас — языки семьи из настроек.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Готово' }))
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(calls.filter((c) => c.method === 'PUT')).toHaveLength(1)
  })

  it('a full save never carries the switch; a failed switch flips back', async () => {
    render(<PlaybackPrefsSheet userId={7} name="Вася" onClose={() => {}} />)
    const sw = await screen.findByRole('switch', { name: 'Выбирать очищенную озвучку' })
    vi.mocked(fetch).mockImplementationOnce(async () => json({ error: 'Нет связи' }, 500))
    await userEvent.click(sw)
    await waitFor(() => expect(sw).toHaveAttribute('aria-checked', 'false'))
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    await userEvent.click(sw)
    await waitFor(() => expect(calls.some((c) => c.url === '/api/users/7/playback-prefs/prefer-cleaned')).toBe(true))
    await userEvent.click(screen.getByRole('radio', { name: 'Сплошной' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'PUT' && c.url === '/api/users/7/playback-prefs')).toBe(true))
    expect(calls.find((c) => c.method === 'PUT' && c.url === '/api/users/7/playback-prefs')!.body).not.toHaveProperty('preferCleaned')
  })

  it('an administrator edits another profile’s', async () => {
    render(<PlaybackPrefsSheet userId={7} name="Вася" onClose={() => {}} />)
    expect(await screen.findByRole('dialog', { name: 'Звук и субтитры: Вася' })).toBeInTheDocument()
    await screen.findByText('Сейчас — языки семьи из настроек.')
    await userEvent.click(screen.getByRole('radio', { name: 'Крупный' }))
    await waitFor(() => expect(calls.some((c) => c.method === 'PUT' && c.url === '/api/users/7/playback-prefs')).toBe(true))
  })
})
