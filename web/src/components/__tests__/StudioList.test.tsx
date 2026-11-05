import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { addStudio, canonicalStudio, KNOWN_STUDIOS, StudioList } from '../StudioList'

function Harness({ initial, onChange }: { initial: string[]; onChange: (s: string[]) => void }) {
  const [list, setList] = useState(initial)
  return (
    <>
      <p id="studios">Любимые озвучки</p>
      <StudioList
        labelledBy="studios"
        studios={list}
        onChange={(s) => {
          setList(s)
          onChange(s)
        }}
      />
    </>
  )
}

const names = () => within(screen.getByRole('list', { name: 'Любимые озвучки' })).queryAllByRole('listitem').map((li) => li.dataset.value)

/** Opens «Добавить студию»: the menu with its field focused. */
async function openAdd() {
  await userEvent.click(screen.getByRole('button', { name: 'Добавить студию' }))
  const menu = screen.getByRole('menu', { name: 'Добавить студию' })
  return { menu, field: within(menu).getByRole('textbox', { name: 'Название студии' }) }
}
const suggestions = () => within(screen.getByRole('menu', { name: 'Добавить студию' })).queryAllByRole('menuitem').map((i) => i.textContent)

describe('StudioList', () => {
  it('adds a suggestion at the end; picked studios are not suggested', async () => {
    const onChange = vi.fn()
    render(<Harness initial={['LostFilm']} onChange={onChange} />)
    const { menu, field } = await openAdd()
    expect(field).toHaveFocus()
    expect(suggestions()).not.toContain('LostFilm')
    await userEvent.click(within(menu).getByRole('menuitem', { name: 'Jaskier' }))
    expect(onChange).toHaveBeenLastCalledWith(['LostFilm', 'Jaskier'])
    expect(names()).toEqual(['LostFilm', 'Jaskier'])
    await waitFor(() => expect(screen.queryByRole('menu')).toBeNull())
    await openAdd()
    expect(suggestions()).not.toContain('Jaskier')
  })

  it('Home and End move the caret in the field, not the focus to the menu', async () => {
    render(<Harness initial={[]} onChange={vi.fn()} />)
    const { field } = await openAdd()
    await userEvent.type(field, 'Lost{Home}')
    expect(field).toHaveFocus()
    await userEvent.keyboard('{End}')
    expect(field).toHaveFocus()
  })

  it('typing filters the suggestions, by any spelling', async () => {
    render(<Harness initial={[]} onChange={vi.fn()} />)
    const { field } = await openAdd()
    await userEvent.type(field, 'куб')
    expect(suggestions()).toEqual(['Кубик в Кубе'])
    await userEvent.clear(field)
    await userEvent.type(field, 'kubik')
    expect(suggestions()).toEqual(['Кубик в Кубе'])
  })

  it('reorders with the handle and removes with −', async () => {
    const onChange = vi.fn()
    render(<Harness initial={['LostFilm', 'Кубик в Кубе', 'Jaskier']} onChange={onChange} />)
    screen.getByRole('button', { name: 'Переместить: Jaskier' }).focus()
    await userEvent.keyboard('{ArrowUp}')
    expect(onChange).toHaveBeenLastCalledWith(['LostFilm', 'Jaskier', 'Кубик в Кубе'])
    await userEvent.click(screen.getByRole('button', { name: 'Убрать: LostFilm' }))
    expect(names()).toEqual(['Jaskier', 'Кубик в Кубе'])
  })

  it('aliases are the same studio as on the server: no duplicates, the canonical spelling', async () => {
    const onChange = vi.fn()
    render(<Harness initial={['LostFilm']} onChange={onChange} />)
    let { field } = await openAdd()
    await userEvent.type(field, 'lost film{Enter}')
    ;({ field } = await openAdd())
    await userEvent.type(field, 'Лостфильм{Enter}')
    expect(onChange).not.toHaveBeenCalled()
    ;({ field } = await openAdd())
    await userEvent.type(field, 'rhs{Enter}')
    expect(onChange).toHaveBeenLastCalledWith(['LostFilm', 'Red Head Sound'])
    await openAdd()
    expect(suggestions()).not.toContain('Red Head Sound')
  })

  it('canonicalStudio', () => {
    expect(canonicalStudio(' lost film ')).toBe('LostFilm')
    expect(canonicalStudio('Lost.Film')).toBe('LostFilm')
    expect(canonicalStudio('Кураж Бамбей')).toBe('Кураж-Бамбей')
    expect(canonicalStudio('кубик³')).toBe('Кубик в Кубе')
    expect(canonicalStudio('Ёжик')).toBeUndefined()
    expect(addStudio(['Гоблин'], 'гоблин')).toEqual(['Гоблин'])
    expect(addStudio([], 'Кураж Бамбей')).toEqual(['Кураж-Бамбей'])
  })

  it('helpers', () => {
    expect(addStudio(['LostFilm'], ' newstudio ')).toEqual(['LostFilm', 'NewStudio'])
    expect(addStudio(['LostFilm'], 'LOSTFILM')).toEqual(['LostFilm'])
    expect(addStudio(Array.from({ length: 30 }, (_, i) => `s${i}`), 'one more')).toHaveLength(30)
    expect(KNOWN_STUDIOS[0]).toBe('LostFilm')
  })
})
