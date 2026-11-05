import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { About, User } from '../../api/types'
import { resetAbout, sourceLink } from '../../state/about'
import { session, useSession } from '../../state/session'
import { AboutSheet, versionLabel } from '../AboutSheet'
import { ProfileMenu } from '../ProfileMenu'
import { SourceLink } from '../SourceLink'

const json = (b: unknown) => new Response(JSON.stringify(b), { status: 200, headers: { 'Content-Type': 'application/json' } })
const official: About = { version: '1.0.0', commit: '0123456789abcdef', license: 'AGPL-3.0-or-later', sourceUrl: 'https://example.org/antresol/tree/v1.0.0' }
const own: About = { version: 'dev', commit: '', license: 'AGPL-3.0-or-later' }
const admin: User = { id: 1, name: 'Папа', avatar: 'grad-1', hasPassword: true, isAdmin: true, isKid: false, maxRating: null }

function serve(about: About, user = admin) {
  const fetch = vi.fn(async (input: RequestInfo | URL) => (String(input).endsWith('/api/about') ? json(about) : json(user)))
  vi.stubGlobal('fetch', fetch)
  return fetch
}

describe('the source offer (AGPL §13)', () => {
  beforeEach(() => {
    resetAbout()
    session.reset()
  })
  afterEach(() => vi.unstubAllGlobals())

  it('links to the source the server names', async () => {
    serve(official)
    render(<SourceLink />)
    const link = await screen.findByRole('link', { name: 'Исходный код · AGPL' })
    expect(link).toHaveAttribute('href', official.sourceUrl)
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  })

  it('shows no link when the server gives none', async () => {
    const fetch = serve(own)
    render(<SourceLink />)
    await waitFor(() => expect(fetch).toHaveBeenCalled())
    await new Promise((r) => setTimeout(r, 0))
    expect(screen.queryByRole('link')).toBeNull()
  })

  it('offers only an http(s) link', () => {
    expect(sourceLink({ ...own, sourceUrl: 'javascript:alert(1)' })).toBe('')
    expect(sourceLink({ ...own, sourceUrl: '' })).toBe('')
    expect(sourceLink(null)).toBe('')
    expect(sourceLink(official)).toBe(official.sourceUrl)
  })

  it('«О сервере» shows the version, the licence, the link and the TMDB notice', async () => {
    serve(official)
    render(<AboutSheet onClose={() => {}} />)
    expect(await screen.findByText('1.0.0 (0123456)')).toBeInTheDocument()
    expect(screen.getByText('AGPL-3.0-or-later')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Исходный код · AGPL' })).toHaveAttribute('href', official.sourceUrl)
    expect(screen.getByRole('link', { name: 'Текст лицензии' })).toHaveAttribute('href', '/api/about/license')
    expect(screen.getByRole('link', { name: 'Сторонние компоненты' })).toHaveAttribute('href', '/api/about/third-party')
    // links that are whole list rows are plain buttons, not in-sentence links (DESIGN.md)
    for (const a of screen.getAllByRole('link')) {
      expect(a).toHaveClass('plain')
      expect(a).not.toHaveClass('link')
    }
    expect(screen.getByText('Этот продукт использует TMDB API, но не одобрен и не сертифицирован TMDB.')).toBeInTheDocument()
  })

  it('«О сервере» without a source link keeps the rest', async () => {
    serve(own)
    render(<AboutSheet onClose={() => {}} />)
    expect(await screen.findByText('dev')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Исходный код · AGPL' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Текст лицензии' })).toBeInTheDocument()
    expect(screen.getByText(/не одобрен и не сертифицирован TMDB/)).toBeInTheDocument()
    expect(versionLabel(own)).toBe('dev')
  })

  it('every profile finds «О сервере…» in its menu', async () => {
    serve(official, { ...admin, id: 2, name: 'Ребёнок', isAdmin: false, isKid: true, hasPassword: false, maxRating: '12+' })
    await session.login(2)
    function Menu() {
      const { user } = useSession()
      return user ? <ProfileMenu user={user} /> : null
    }
    render(
      <MemoryRouter>
        <Menu />
      </MemoryRouter>,
    )
    await userEvent.click(screen.getByRole('button', { name: /Ребёнок/ }))
    await userEvent.click(screen.getByRole('menuitem', { name: 'О сервере…' }))
    expect(await screen.findByRole('dialog', { name: 'О сервере' })).toBeInTheDocument()
    expect(await screen.findByRole('link', { name: 'Исходный код · AGPL' })).toBeInTheDocument()
  })
})
