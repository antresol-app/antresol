import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { SegmentedControl } from '../SegmentedControl'

describe('SegmentedControl shortLabels', () => {
  it('shows the short labels at every width; the full word is the name and the tooltip', () => {
    render(
      <SegmentedControl
        shortLabels
        label="Размер"
        value="m"
        onChange={() => {}}
        segments={[
          { value: 'm', label: 'Средний', short: 'M' },
          { value: 'xl', label: 'Очень крупный', short: 'XL' },
        ]}
      />,
    )
    const xl = screen.getByRole('radio', { name: 'Очень крупный' })
    expect(xl).toHaveTextContent('XL')
    expect(xl).not.toHaveTextContent('Очень')
    expect(xl).toHaveAttribute('title', 'Очень крупный')
  })
})
