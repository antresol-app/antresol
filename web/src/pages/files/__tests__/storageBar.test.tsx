// The sidebar's «Хранилище» reads the usage report like «Диск» in the settings
// does: the same names and colours, an addon's segment by the server's label.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { StorageBar, resetUsageCache } from '../StorageBar'

const usage = {
  total: 1000,
  used: 600,
  free: 400,
  categories: [
    { id: 'movies', bytes: 300 },
    { id: 'addon:dl:data', bytes: 200, label: 'Загрузки' },
    { id: 'books', bytes: 0 },
    { id: 'other', bytes: 100 },
  ],
}

beforeEach(() => {
  resetUsageCache()
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(usage), { status: 200, headers: { 'Content-Type': 'application/json' } })))
})
afterEach(() => vi.unstubAllGlobals())

describe('StorageBar', () => {
  it('lists the categories that hold something, an addon’s by its own name and an addon colour', async () => {
    render(<StorageBar large />)
    expect(await screen.findByText('Загрузки')).toBeInTheDocument()
    const names = [...document.querySelectorAll('li')].map((li) => li.children[1]?.textContent)
    expect(names).toEqual(['Фильмы', 'Загрузки', 'Другое', 'Свободно'])
    const dots = [...document.querySelectorAll<HTMLElement>('li > span:first-child')].map((d) => d.style.background)
    expect(dots.slice(0, 3)).toEqual(['var(--blue)', 'var(--green)', 'var(--gray)'])
  })

  it('draws the bar with one segment per category, as wide as its share', async () => {
    render(<StorageBar large />)
    const bar = await screen.findByRole('img', { name: /Занято/ })
    expect([...bar.children].map((s) => (s as HTMLElement).style.width)).toEqual(['30%', '20%', '10%'])
  })
})
