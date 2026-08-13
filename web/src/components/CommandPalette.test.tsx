import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../api'
import { CommandPalette } from './CommandPalette'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('CommandPalette', () => {
  it('opens on Ctrl+K and can jump to search', async () => {
    vi.spyOn(api, 'projects').mockResolvedValue([])
    render(
      <MemoryRouter>
        <CommandPalette />
      </MemoryRouter>,
    )
    expect(screen.queryByRole('dialog', { name: 'Command palette' })).not.toBeInTheDocument()
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true })
    expect(await screen.findByRole('dialog', { name: 'Command palette' })).toBeInTheDocument()
    expect(screen.getByText('Search the index')).toBeInTheDocument()
  })
})
