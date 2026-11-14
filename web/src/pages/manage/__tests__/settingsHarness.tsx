// «Настройки сервера» in a router, on one of its screens; go() opens another
// one in the same render (the page keeps what it loaded).
import { act, render } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router'
import { Settings, type SettingsGroup } from '../Settings'

export function renderSettings(group?: SettingsGroup, hash = '') {
  let nav: ((to: string) => void) | undefined
  function Grab() {
    nav = useNavigate()
    return null
  }
  const path = group ? `/manage/settings/${group}${hash}` : '/manage/settings'
  const r = render(
    <MemoryRouter initialEntries={[path]}>
      <Grab />
      <Routes>
        <Route path="/manage/settings/:group?" element={<Settings />} />
      </Routes>
    </MemoryRouter>,
  )
  return { ...r, go: (g?: SettingsGroup) => act(() => nav!(g ? `/manage/settings/${g}` : '/manage/settings')) }
}
