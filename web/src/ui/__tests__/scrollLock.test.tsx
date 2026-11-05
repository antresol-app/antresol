import { describe, expect, it } from 'vitest'
import { render } from '@testing-library/react'
import { Sheet } from '../Sheet'

const html = () => document.documentElement.style

describe('a sheet locks the page behind it', () => {
  it('the page does not scroll while any sheet is open, and gets back what it had', () => {
    html().overflow = 'clip'
    const sheet = (title: string) => <Sheet title={title} onCancel={() => {}} actions={[{ label: 'Отменить', kind: 'cancel', run: () => {} }]} />
    const a = render(sheet('Первый'))
    expect(html().overflow).toBe('hidden')
    expect(html().overscrollBehavior).toBe('none')
    const b = render(sheet('Второй'))
    a.unmount()
    expect(html().overflow).toBe('hidden') // the other sheet is still open
    b.unmount()
    expect(html().overflow).toBe('clip')
    expect(html().overscrollBehavior).toBe('')
    html().overflow = ''
  })
})
