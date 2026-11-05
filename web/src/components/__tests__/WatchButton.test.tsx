import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { WatchButton } from '../WatchButton'

const ways = () => [
  { id: 'here', label: 'Здесь', run: vi.fn() },
  { id: 'tv', label: 'На телевизоре', run: vi.fn() },
]

describe('WatchButton', () => {
  it('is a single plain button with one way and no extras', () => {
    const w = [ways()[0]!]
    render(<WatchButton label="Смотреть" ways={w} current="here" />)
    const b = screen.getByRole('button', { name: 'Смотреть' })
    fireEvent.click(b)
    expect(w[0]!.run).toHaveBeenCalledTimes(1)
    expect(document.querySelector('[aria-haspopup]')).toBeNull()
    expect(screen.getAllByRole('button')).toHaveLength(1)
  })

  it('runs the current way from the main part', () => {
    const w = ways()
    render(<WatchButton label="Смотреть" ways={w} current="tv" />)
    fireEvent.click(screen.getByRole('button', { name: 'Смотреть' }))
    expect(w[1]!.run).toHaveBeenCalledTimes(1)
    expect(w[0]!.run).not.toHaveBeenCalled()
  })

  it('lists the ways as radios with the current one checked', () => {
    render(<WatchButton label="Смотреть" ways={ways()} current="here" />)
    fireEvent.click(screen.getByRole('button', { name: 'Другие способы просмотра' }))
    const radios = screen.getAllByRole('menuitemradio')
    expect(radios.map((r) => r.textContent)).toEqual(['Здесь', 'На телевизоре'])
    expect(radios[0]).toHaveAttribute('aria-checked', 'true')
    expect(radios[1]).toHaveAttribute('aria-checked', 'false')
  })

  it('runs a chosen way and reports it', () => {
    const w = ways()
    const onChoose = vi.fn()
    render(<WatchButton label="Смотреть" ways={w} current="here" onChoose={onChoose} />)
    fireEvent.click(screen.getByRole('button', { name: 'Другие способы просмотра' }))
    fireEvent.click(screen.getByRole('menuitemradio', { name: 'На телевизоре' }))
    expect(w[1]!.run).toHaveBeenCalledTimes(1)
    expect(onChoose).toHaveBeenCalledWith('tv')
  })

  it('shows extras as menu items after the radios', () => {
    const run = vi.fn()
    render(<WatchButton label="Смотреть" ways={ways()} current="here" extra={[{ label: 'Проверить', run }]} />)
    fireEvent.click(screen.getByRole('button', { name: 'Другие способы просмотра' }))
    const items = screen.getAllByRole('menuitem')
    expect(items.map((i) => i.textContent)).toEqual(['Проверить'])
    const all = Array.from(screen.getByRole('menu').querySelectorAll('[role^="menuitem"]'))
    expect(all.map((i) => i.textContent)).toEqual(['Здесь', 'На телевизоре', 'Проверить'])
    expect(screen.getByRole('separator')).toBeInTheDocument()
    fireEvent.click(items[0]!)
    expect(run).toHaveBeenCalledTimes(1)
  })

  it('does not report an extra item as a chosen way', () => {
    const onChoose = vi.fn()
    const run = vi.fn()
    render(<WatchButton label="Смотреть" ways={ways()} current="here" onChoose={onChoose} extra={[{ label: 'Проверить', run }]} />)
    fireEvent.click(screen.getByRole('button', { name: 'Другие способы просмотра' }))
    fireEvent.click(screen.getByRole('menuitem', { name: 'Проверить' }))
    expect(run).toHaveBeenCalledTimes(1)
    expect(onChoose).not.toHaveBeenCalled()
  })

  it('opens a menu for a single way with extras', () => {
    render(<WatchButton label="Смотреть" ways={[ways()[0]!]} current="here" extra={[{ label: 'Проверить', run: vi.fn() }]} />)
    expect(screen.getByRole('button', { name: 'Другие способы просмотра' })).toHaveAttribute('aria-haspopup', 'menu')
  })

  it('shows the spinner and disables both parts while loading', () => {
    render(<WatchButton label="Смотреть" ways={ways()} current="here" loading />)
    const main = screen.getByRole('button', { name: 'Смотреть' })
    expect(main).toBeDisabled()
    expect(main).toHaveAttribute('aria-busy', 'true')
    expect(screen.getByRole('button', { name: 'Другие способы просмотра' })).toBeDisabled()
  })

  it('disables both parts when disabled', () => {
    render(<WatchButton label="Смотреть" ways={ways()} current="here" disabled />)
    expect(screen.getByRole('button', { name: 'Смотреть' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Другие способы просмотра' })).toBeDisabled()
  })
})
