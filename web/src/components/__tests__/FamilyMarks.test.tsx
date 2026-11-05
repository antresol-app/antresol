import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { FamilyMember } from '../../api/types'
import type { FamilyPlace } from '../../player/familyMarks'
import { FamilyMarks, ReturnPill } from '../FamilyMarks'

const place = (userId: number, name: string, at: number, label: string): FamilyPlace => ({
  member: { userId, name, avatar: 'grad-3', isKid: false, itemId: 1, positionMs: 0, durationMs: 0, fraction: 0, finished: false, updatedAt: '' } as FamilyMember,
  at,
  label,
})

describe('FamilyMarks', () => {
  it('is not there without places', () => {
    const { container } = render(<FamilyMarks places={[]} total={100} onJump={() => {}} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('a face per place, a click jumps there', async () => {
    const onJump = vi.fn()
    render(<FamilyMarks places={[place(2, 'Мама', 120, 'стр. 120'), place(3, 'Папа', 30, 'стр. 30')]} total={300} onJump={onJump} />)
    const buttons = screen.getAllByRole('button')
    // in timeline order
    expect(buttons.map((b) => b.getAttribute('aria-label'))).toEqual(['Перейти на место профиля «Папа» — стр. 30', 'Перейти на место профиля «Мама» — стр. 120'])
    expect(buttons.map((b) => b.dataset.pct)).toEqual(['10', '40'])
    await userEvent.click(buttons[1]!)
    expect(onJump).toHaveBeenCalledWith(expect.objectContaining({ at: 120, label: 'стр. 120' }))
  })

  it('runs right to left for a manga', () => {
    render(<FamilyMarks places={[place(2, 'Мама', 25, 'стр. 26')]} total={100} dir="rtl" onJump={() => {}} />)
    expect(screen.getByRole('button').dataset.pct).toBe('75')
  })
})

describe('ReturnPill', () => {
  it('goes back, or is dismissed', async () => {
    const onReturn = vi.fn()
    const onDismiss = vi.fn()
    render(<ReturnPill label="стр. 12" onReturn={onReturn} onDismiss={onDismiss} />)
    await userEvent.click(screen.getByRole('button', { name: 'Вернуться к стр. 12' }))
    expect(onReturn).toHaveBeenCalledTimes(1)
    await userEvent.click(screen.getByRole('button', { name: 'Скрыть' }))
    expect(onDismiss).toHaveBeenCalledTimes(1)
  })
})
