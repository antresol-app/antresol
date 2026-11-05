// Final review, fix C: library series seasons, «Оставить на сервере»,
// editable sources, upload limit field, hero focus, who added a download.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { Card } from '../../api/types'
import { Hero } from '../../components/Hero'

afterEach(() => vi.unstubAllGlobals())

describe('F32: hero arrow keys keep the keyboard focus', () => {
  const card = (id: number, title: string): Card => ({ id, kind: 'movie', title, subtitle: '', poster: '', backdrop: '', logo: '', progress: 0, onServer: true })
  it('the same button of the next slide is focused', async () => {
    render(
      <MemoryRouter>
        <Hero items={[card(1, 'Брат'), card(2, 'Брат 2')]} />
      </MemoryRouter>,
    )
    const more = screen.getByRole('button', { name: 'Подробнее' })
    more.focus()
    await userEvent.keyboard('{ArrowRight}')
    await waitFor(() => expect(screen.getByRole('group', { name: /2 из 2/ })).toBeInTheDocument())
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Подробнее' })))
    expect(document.activeElement).not.toBe(document.body)
  })
})
