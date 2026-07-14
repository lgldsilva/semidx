import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { Tabs } from './Tabs'

const tabs = [
  { id: 'overview', label: 'Overview' },
  { id: 'files', label: 'Files' },
  { id: 'chat', label: 'Chat' },
] as const

describe('Tabs', () => {
  it('renders an accessible tablist with aria-selected on the active tab', () => {
    render(<Tabs tabs={tabs} active="files" onSelect={() => {}} label="Project sections" />)
    expect(screen.getByRole('tablist', { name: 'Project sections' })).toBeInTheDocument()
    expect(screen.getAllByRole('tab')).toHaveLength(3)
    expect(screen.getByRole('tab', { name: 'Files' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: 'Overview' })).toHaveAttribute('aria-selected', 'false')
  })

  it('calls onSelect with the clicked tab id', () => {
    const onSelect = vi.fn()
    render(<Tabs tabs={tabs} active="overview" onSelect={onSelect} />)
    fireEvent.click(screen.getByRole('tab', { name: 'Chat' }))
    expect(onSelect).toHaveBeenCalledWith('chat')
  })
})
