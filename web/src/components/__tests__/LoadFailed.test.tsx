import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { LoadFailed } from '../LoadFailed'

describe('LoadFailed', () => {
  it('says what failed, with the server’s words, and retries', async () => {
    const retry = vi.fn()
    render(<LoadFailed title="Не удалось загрузить" message="Сервер недоступен" retry="Повторить" onRetry={retry} />)
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('Не удалось загрузить')
    expect(alert).toHaveTextContent('Сервер недоступен')
    expect(screen.getByRole('heading', { name: 'Не удалось загрузить' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Повторить' }))
    expect(retry).toHaveBeenCalledOnce()
  })
})
