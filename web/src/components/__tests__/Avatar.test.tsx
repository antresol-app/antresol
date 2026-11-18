import { describe, expect, it } from 'vitest'
import { fireEvent, render } from '@testing-library/react'
import { Avatar } from '../Avatar'

describe('Avatar', () => {
  it('is the initial on the profile’s gradient', () => {
    const { container } = render(<Avatar name="маша" avatar="grad-3" />)
    const el = container.firstElementChild as HTMLElement
    expect(el).toHaveTextContent('М')
    expect(el.style.background).toContain('--grad-3')
    expect(container.querySelector('img')).toBeNull()
  })

  it('draws the photo in place of the letter, on the same gradient (it shows while the picture loads)', () => {
    const { container } = render(<Avatar name="Маша" avatar="grad-3" photo="/api/profiles/4/photo?v=9" size={40} />)
    const img = container.querySelector('img')!
    expect(img).toHaveAttribute('src', '/api/profiles/4/photo?v=9')
    expect(img).toHaveAttribute('alt', '')
    expect(container.firstElementChild).not.toHaveTextContent('М')
    expect((container.firstElementChild as HTMLElement).style.background).toContain('--grad-3')
  })

  it('goes back to the letter when the picture does not load, and tries a new photo again', () => {
    const { container, rerender } = render(<Avatar name="Маша" avatar="grad-3" photo="/p?v=1" />)
    fireEvent.error(container.querySelector('img')!)
    expect(container.querySelector('img')).toBeNull()
    expect(container.firstElementChild).toHaveTextContent('М')
    rerender(<Avatar name="Маша" avatar="grad-3" photo="/p?v=2" />)
    expect(container.querySelector('img')).toHaveAttribute('src', '/p?v=2')
  })

  it('is hidden from assistive tech (the name is beside it)', () => {
    const { container } = render(<Avatar name="Маша" avatar="grad-3" photo="/p" />)
    expect(container.firstElementChild).toHaveAttribute('aria-hidden', 'true')
  })
})
