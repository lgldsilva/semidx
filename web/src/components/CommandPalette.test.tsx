import { fireEvent, render, screen, waitFor } from '@testing-library/react'
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

  it('exposes combobox semantics and restores focus on Escape', async () => {
    vi.spyOn(api, 'projects').mockResolvedValue([])
    const trigger = document.createElement('button')
    document.body.appendChild(trigger)
    trigger.focus()
    render(
      <MemoryRouter>
        <CommandPalette />
      </MemoryRouter>,
    )

    fireEvent.keyDown(window, { key: 'k', ctrlKey: true })
    expect(await screen.findByRole('combobox')).toHaveAttribute('aria-controls', 'command-results')
    expect(screen.getByRole('listbox')).toBeInTheDocument()
    expect(screen.getAllByRole('option')[0]).toHaveAttribute('aria-selected', 'true')

    fireEvent.keyDown(window, { key: 'Escape' })
    await waitFor(() => expect(trigger).toHaveFocus())
    trigger.remove()
  })
})
