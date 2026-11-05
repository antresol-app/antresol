import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { autoDetail, QualityMenu } from '../QualityMenu'

describe('QualityMenu (F13, F17)', () => {
  it('offers Авто / Оригинал / 1080p / 720p / 480p and reports the choice', async () => {
    const onChange = vi.fn()
    render(<QualityMenu quality="auto" info={{ selected: 'auto', applied: '1080', remote: true }} onChange={onChange} />)
    const trigger = screen.getByRole('button', { name: 'Качество: Авто · 1080p' })
    await userEvent.click(trigger)
    const items = screen.getAllByRole('menuitemradio')
    expect(items.map((i) => i.textContent)).toEqual([
      expect.stringContaining('Авто'),
      expect.stringContaining('Оригинал'),
      expect.stringContaining('1080p'),
      expect.stringContaining('720p'),
      expect.stringContaining('480p'),
    ])
    expect(items[0]).toHaveAttribute('aria-checked', 'true')
    expect(items[0]).toHaveTextContent('Вне дома — до 1080p')
    await userEvent.click(screen.getByRole('menuitemradio', { name: /720p/ }))
    expect(onChange).toHaveBeenCalledWith('720')
  })

  it('says what «Авто» means', () => {
    expect(autoDetail({ selected: 'auto', applied: 'original', remote: false })).toBe('Дома — как в файле')
    expect(autoDetail({ selected: 'auto', applied: '720', remote: true })).toBe('Вне дома — до 720p')
    expect(autoDetail(null)).toBe('Подбирается само')
  })
})
