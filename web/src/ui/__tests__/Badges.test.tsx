// Badges: a list of capsules, or spans inside a button's row (`inline`).
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Badges } from '../Badge'

describe('Badges', () => {
  it('is a list by default', () => {
    render(<Badges items={['4K', 'HDR']} />)
    expect(screen.getAllByRole('listitem').map((li) => li.textContent)).toEqual(['4K', 'HDR'])
  })

  it('inline: spans with the same badge style, valid inside a button', () => {
    render(
      <button type="button">
        <Badges inline items={['1080p', 'Звук RU']} className="mine" />
      </button>,
    )
    const button = screen.getByRole('button')
    expect(button.querySelector('ul, li')).toBeNull()
    const row = button.querySelector('.mine')!
    expect(row.classList.contains('badges')).toBe(true)
    expect([...row.children].map((c) => c.className)).toEqual(['badge', 'badge'])
    expect(button).toHaveTextContent('1080p')
    expect(button).toHaveTextContent('Звук RU')
  })
})
