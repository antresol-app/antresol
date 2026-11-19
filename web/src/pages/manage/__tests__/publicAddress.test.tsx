// «Адрес сервера» in «Настройки сервера»: the server's external address,
// saved by itself (PUT /api/settings publicUrl), read-only when the server
// has VAULT_PUBLIC_URL; one row of «Управление»'s server part.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { Settings as Values } from '../../../api/types'
import { MANAGE_SECTIONS } from '../Manage'
import { renderSettings } from './settingsHarness'

const json = (b: unknown, status = 200) => new Response(JSON.stringify(b), { status, headers: { 'Content-Type': 'application/json' } })
const base: Values = { quality: 1080, voice: 'dub', remoteQuality: '1080', remoteMusic: 'high', autoSubtitles: true, subtitleLangs: ['ru', 'en'], subtitlesAvailable: false }

function stub(values: Values) {
  const puts: unknown[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input)
      if (url.endsWith('/api/settings') && init?.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as Values
        puts.push(body)
        return json({ ...body, publicUrl: (body.publicUrl ?? '').replace(/\/+$/, '') })
      }
      if (url.endsWith('/api/settings')) return json(values)
      return json({})
    }),
  )
  return puts
}

afterEach(() => vi.unstubAllGlobals())

describe('Настройки сервера: Адрес сервера', () => {
  it('is a row of the server part', () => {
    expect(MANAGE_SECTIONS.filter((s) => s.to === 'settings/address' && s.group === 'server')).toHaveLength(1)
  })

  it('saves the address on leaving the field', async () => {
    const puts = stub(base)
    renderSettings('address')
    expect(await screen.findByRole('heading', { name: 'Адрес сервера', level: 2 })).toBeInTheDocument()
    const field = screen.getByRole('textbox', { name: 'Внешний адрес сервера' })
    await userEvent.type(field, 'https://media.example.org/')
    fireEvent.blur(field)
    await waitFor(() => expect(puts).toHaveLength(1))
    expect((puts[0] as Values).publicUrl).toBe('https://media.example.org/')
    expect(await screen.findByDisplayValue('https://media.example.org')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Как открыть сервер снаружи' })).toHaveAttribute('href', 'https://antresol-app.github.io/antresol/configuration.html')
  })

  it('VAULT_PUBLIC_URL on the server: shown, read-only, and said so', async () => {
    stub({ ...base, publicUrlFromEnv: 'https://env.example.org' })
    renderSettings('address')
    expect(await screen.findByDisplayValue('https://env.example.org')).toHaveAttribute('readonly')
    expect(screen.getByText('Задан на сервере (VAULT_PUBLIC_URL)')).toBeInTheDocument()
  })
})
