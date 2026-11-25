// The kit parts of the foundation batch: Spinner, EmptyState, Skeleton, Slider,
// ListSelectRow, Checkbox's extensions, TextArea, Chips (and the two names it
// keeps for older callers), and what the addon SDK hands out as `ui`.
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Button } from '../Button'
import { Checkbox } from '../Checkbox'
import { Chips } from '../Chips'
import { EmptyState } from '../EmptyState'
import { FilterChips } from '../FilterChips'
import { IconButton } from '../IconButton'
import { GroupedSection, ListSelectRow } from '../List'
import { Sheet } from '../Sheet'
import { Skeleton } from '../Skeleton'
import { Slider } from '../Slider'
import { Spinner } from '../Spinner'
import { TextArea, TextField } from '../TextField'
import { ToggleChips } from '../ToggleChips'
import * as sdkUi from '../../addons/sdkUi'
import { css, rule } from './cssRule'

describe('Spinner', () => {
  it('is decoration without a label and a named image with one', () => {
    const { container, rerender } = render(<Spinner />)
    const ring = container.firstElementChild as HTMLElement
    expect(ring).toHaveAttribute('aria-hidden', 'true')
    expect(ring).toHaveClass('spin')
    rerender(<Spinner label="Загрузка" size="lg" track />)
    expect(screen.getByRole('img', { name: 'Загрузка' })).toHaveClass('spin', 'lg', 'track')
  })

  it('is what a busy Button, IconButton and Sheet draw (one ring, one keyframes)', () => {
    const { container } = render(
      <>
        <Button loading>Сохранить</Button>
        <IconButton icon="plus" label="Добавить" loading />
      </>,
    )
    expect(container.querySelectorAll('.spin')).toHaveLength(2)
    expect(screen.getByRole('button', { name: 'Сохранить' })).toHaveAttribute('aria-busy', 'true')
    render(<Sheet title="Удалить?" busy actions={[{ label: 'Отменить', kind: 'cancel', run: () => {} }, { label: 'Удалить', kind: 'destructive', run: () => {} }]} onCancel={() => {}} />)
    expect(screen.getByRole('img', { name: 'Подождите' })).toHaveClass('spin')
  })
})

describe('EmptyState', () => {
  it('has the title as a heading, the hint, the actions, and reads out a failure', () => {
    render(<EmptyState role="alert" icon={<svg data-testid="glyph" />} title="Пусто" hint="Добавьте файлы" actions={<Button>Повторить</Button>} />)
    expect(screen.getByRole('alert')).toContainElement(screen.getByRole('heading', { level: 2, name: 'Пусто' }))
    expect(screen.getByText('Добавьте файлы')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Повторить' })).toBeInTheDocument()
    expect(screen.getByTestId('glyph')).toBeInTheDocument()
  })

  it('takes the heading level of its page', () => {
    render(<EmptyState as="h1" title="Не найдено" />)
    expect(screen.getByRole('heading', { level: 1 })).toBeInTheDocument()
  })
})

describe('Skeleton', () => {
  it('is hidden from assistive tech and keeps the caller’s class and style', () => {
    const { container } = render(<Skeleton tone="soft" className="mine" style={{ width: 20 }} />)
    const el = container.firstElementChild as HTMLElement
    expect(el).toHaveAttribute('aria-hidden', 'true')
    expect(el).toHaveClass('mine', 'soft')
    expect(el.style.width).toBe('20px')
  })
})

describe('Slider', () => {
  it('is a named range input that reports numbers and carries its fraction for the track', () => {
    const onChange = vi.fn()
    render(<Slider label="Громкость" value={0.25} onChange={onChange} valueText="25%" />)
    const input = screen.getByRole('slider', { name: 'Громкость' })
    expect(input).toHaveAttribute('aria-valuetext', '25%')
    expect(input.style.getPropertyValue('--v')).toBe('0.25')
    fireEvent.change(input, { target: { value: '0.5' } })
    expect(onChange).toHaveBeenCalledWith(0.5)
  })

  it('works over any range, and keeps the fraction inside 0…1', () => {
    render(<Slider label="Скорость" min={50} max={150} step={10} value={200} onChange={() => {}} />)
    expect(screen.getByRole('slider').style.getPropertyValue('--v')).toBe('1')
  })
})

describe('ListSelectRow', () => {
  it('marks the chosen row pressed with a check and only calls for an enabled one', async () => {
    const pick = vi.fn()
    const { container } = render(
      <GroupedSection>
        <ListSelectRow label="1080p" hint="RU" selected onSelect={() => pick('a')} />
        <ListSelectRow label="720p" selected={false} onSelect={() => pick('b')} />
        <ListSelectRow label="Нет сигнала" selected={false} disabled onSelect={() => pick('c')} />
        <ListSelectRow label="Другой…" onSelect={() => pick('d')} />
      </GroupedSection>,
    )
    expect(screen.getByRole('button', { name: /1080p/ })).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', { name: /720p/ })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByRole('button', { name: 'Другой…' })).not.toHaveAttribute('aria-pressed')
    // a check is drawn for the chosen row only
    expect(container.querySelectorAll('svg')).toHaveLength(1)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: /720p/ }))
    await user.click(screen.getByRole('button', { name: /Нет сигнала/ }))
    await user.click(screen.getByRole('button', { name: 'Другой…' }))
    expect(pick.mock.calls).toEqual([['b'], ['d']])
  })
})

describe('Checkbox', () => {
  it('shows a group as indeterminate (the property, which no attribute can set) and follows it', () => {
    const { rerender } = render(<Checkbox label="Все" checked={false} indeterminate onChange={() => {}} />)
    const box = screen.getByRole('checkbox', { name: 'Все' }) as HTMLInputElement
    expect(box.indeterminate).toBe(true)
    rerender(<Checkbox label="Все" checked indeterminate={false} onChange={() => {}} />)
    expect(box.indeterminate).toBe(false)
  })

  it('stays indeterminate while the caller refuses the press (the browser clears it on its own)', async () => {
    const onChange = vi.fn()
    render(<Checkbox label="Все" checked={false} indeterminate onChange={onChange} />)
    const box = screen.getByRole('checkbox', { name: 'Все' }) as HTMLInputElement
    await userEvent.setup().click(box)
    expect(onChange).toHaveBeenCalledWith(true)
    expect(box.indeterminate).toBe(true)
    expect(box.checked).toBe(false)
  })

  it('has no visible label in a table row: aria-label names it', async () => {
    const onChange = vi.fn()
    render(<Checkbox aria-label="Выбрать строку" checked={false} onChange={onChange} />)
    await userEvent.setup().click(screen.getByRole('checkbox', { name: 'Выбрать строку' }))
    expect(onChange).toHaveBeenCalledWith(true)
  })

  it('describes itself with the line under the label', () => {
    render(<Checkbox label="Сохранить" description="Останется в профиле" checked={false} onChange={() => {}} />)
    expect(screen.getByRole('checkbox', { name: 'Сохранить' })).toHaveAccessibleDescription('Останется в профиле')
  })

  it('as a card, the whole card is the box’s label and its target; an off card is not dimmed whole', async () => {
    const onChange = vi.fn()
    const { rerender } = render(
      <Checkbox card className="surface" label={<span><span>Старый сервис</span><span>Найдена база</span></span>} checked={false} onChange={onChange} />,
    )
    const box = screen.getByRole('checkbox', { name: /Старый сервис.*Найдена база/ })
    await userEvent.setup().click(screen.getByText('Найдена база'))
    expect(onChange).toHaveBeenCalledWith(true)
    rerender(<Checkbox card className="surface" label="Старый сервис" checked={false} disabled onChange={onChange} />)
    expect(box.closest('label')).toHaveAttribute('data-disabled')
    // the kit's .45 is for a plain row; a card dims its own parts (a rule after the plain one, at the same weight)
    expect(rule('ui/Checkbox.module.css', '.card[data-disabled]')).toMatch(/opacity:\s*1\b/)
    expect(css('ui/Checkbox.module.css').indexOf('.card[data-disabled]')).toBeGreaterThan(css('ui/Checkbox.module.css').indexOf('.checkbox[data-disabled]'))
  })
})

describe('TextArea', () => {
  it('is a labelled multi-line field with its hint, and reads out an error instead', () => {
    const { rerender } = render(<TextArea label="Заметка" hint="Видна только вам" />)
    const area = screen.getByRole('textbox', { name: 'Заметка' })
    expect(area.tagName).toBe('TEXTAREA')
    expect(area).toHaveAttribute('rows', '4')
    expect(area).toHaveAccessibleDescription('Видна только вам')
    rerender(<TextArea label="Заметка" hint="Видна только вам" error="Слишком длинно" />)
    expect(screen.getByRole('alert')).toHaveTextContent('Слишком длинно')
    expect(area).toHaveAttribute('aria-invalid', 'true')
  })

  it('TextField keeps its hint and error wiring through the shared line', () => {
    render(<TextField label="Время" type="time" error="Нужно время" defaultValue="22:00" />)
    const field = screen.getByLabelText('Время')
    expect(field).toHaveAttribute('type', 'time')
    expect(field).toHaveAccessibleDescription('Нужно время')
  })
})

describe('Chips', () => {
  const chips = [
    { value: 'a', label: 'Все', count: 3 },
    { value: 'b', label: 'Спорт' },
    { value: 'c', label: 'Кино' },
  ]

  it('one choice: radio buttons, one tab stop, the arrow keys move the choice', async () => {
    const onChange = vi.fn()
    render(<Chips label="Группы" chips={chips} value="a" onChange={onChange} />)
    expect(screen.getAllByRole('radio').map((r) => r.getAttribute('tabindex'))).toEqual(['0', '-1', '-1'])
    expect(screen.getByRole('radio', { name: /Все/ })).toHaveAttribute('aria-checked', 'true')
    screen.getByRole('radio', { name: /Все/ }).focus()
    await userEvent.setup().keyboard('{End}')
    expect(onChange).toHaveBeenCalledWith('c')
  })

  it('keeps one tab stop when no chip is chosen (the first)', () => {
    render(<Chips label="Группы" chips={chips} value={'' as 'a'} onChange={() => {}} />)
    expect(screen.getAllByRole('radio').map((r) => r.getAttribute('tabindex'))).toEqual(['0', '-1', '-1'])
  })

  it('several: toggles that report the one pressed, with the children after them', async () => {
    const onChange = vi.fn()
    render(
      <Chips multiple label="Жанры" chips={chips} value={['b']} onChange={onChange}>
        <button type="button">Ещё…</button>
      </Chips>,
    )
    const group = screen.getByRole('group', { name: 'Жанры' })
    expect(group.lastElementChild).toHaveTextContent('Ещё…')
    expect(screen.getByRole('button', { name: 'Спорт' })).toHaveAttribute('aria-pressed', 'true')
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Спорт' }))
    await user.click(screen.getByRole('button', { name: 'Кино' }))
    expect(onChange.mock.calls).toEqual([['b', false], ['c', true]])
  })

  it('only the one-choice chips scroll sideways on a phone; toggles always wrap', () => {
    const sheet = css('ui/Chips.module.css')
    expect(sheet).toMatch(/\.scroller \.chips \{[^}]*flex-wrap: nowrap/)
    expect(sheet).not.toMatch(/\n  \.chips \{[^}]*nowrap/)
  })

  it('FilterChips and ToggleChips are the same component under their old names', async () => {
    const pick = vi.fn()
    const toggle = vi.fn()
    render(
      <>
        <FilterChips label="Группы" chips={chips} value="a" onChange={pick} />
        <ToggleChips label="Жанры" chips={chips} value={[]} onToggle={toggle} />
      </>,
    )
    expect(screen.getByRole('radiogroup', { name: 'Группы' })).toBeInTheDocument()
    await userEvent.setup().click(within(screen.getByRole('group', { name: 'Жанры' })).getByRole('button', { name: 'Кино' }))
    expect(toggle).toHaveBeenCalledWith('c', true)
  })
})

describe('the addon SDK’s ui', () => {
  it('hands out the kit parts', () => {
    for (const name of ['Spinner', 'EmptyState', 'Skeleton', 'Slider', 'ListSelectRow', 'Checkbox', 'TextArea', 'Chips', 'FilterChips', 'ToggleChips', 'RankedList']) {
      expect(sdkUi, name).toHaveProperty(name)
    }
  })
})
