import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { GraphCanvas } from './GraphCanvas'
import type { GraphNode } from '../../api'

const nodes: GraphNode[] = [
  { id: 'a.go', label: 'a.go', kind: 'file', seed: true, depth: 0 },
]

describe('GraphCanvas', () => {
  it('survives an empty-to-populated transition without crashing', () => {
    const { rerender } = render(<GraphCanvas nodes={[]} edges={[]} />)
    expect(screen.getByText('No graph nodes to display.')).toBeInTheDocument()
    rerender(<GraphCanvas nodes={nodes} edges={[]} />)
    expect(screen.getByLabelText('Dependency graph')).toBeInTheDocument()
    rerender(<GraphCanvas nodes={[]} edges={[]} />)
    expect(screen.getByText('No graph nodes to display.')).toBeInTheDocument()
  })
})

describe('GraphCanvas keyboard access', () => {
  const twoNodes: GraphNode[] = [
    { id: 'a.go', label: 'a.go', kind: 'file', seed: true, depth: 0 },
    { id: 'pkg/b', label: 'pkg/b', kind: 'package', seed: false, depth: 1 },
  ]

  it('exposes each node as a focusable button with an accessible name', () => {
    render(<GraphCanvas nodes={twoNodes} edges={[]} />)
    const seed = screen.getByRole('button', { name: 'file a.go (seed)' })
    const pkg = screen.getByRole('button', { name: 'package pkg/b' })
    expect(seed).toHaveAttribute('tabindex', '0')
    expect(pkg).toHaveAttribute('tabindex', '0')
  })

  it('selects with Enter and Space, and focuses with Shift+Enter', () => {
    const onSelect = vi.fn()
    const onFocus = vi.fn()
    render(<GraphCanvas nodes={twoNodes} edges={[]} onSelect={onSelect} onFocus={onFocus} />)

    const node = screen.getByRole('button', { name: 'package pkg/b' })
    node.focus()
    expect(node).toHaveFocus()

    fireEvent.keyDown(node, { key: 'Enter' })
    expect(onSelect).toHaveBeenCalledWith('pkg/b', 'package')

    fireEvent.keyDown(node, { key: ' ' })
    expect(onSelect).toHaveBeenCalledTimes(2)

    fireEvent.keyDown(node, { key: 'Shift' })
    fireEvent.keyDown(node, { key: 'Enter', shiftKey: true })
    expect(onFocus).toHaveBeenCalledWith('pkg/b', 'package')
    // Shift+Enter focuses instead of selecting.
    expect(onSelect).toHaveBeenCalledTimes(2)
  })

  it('marks the selected node with aria-pressed', () => {
    render(<GraphCanvas nodes={twoNodes} edges={[]} selected="a.go" />)
    expect(screen.getByRole('button', { name: 'file a.go (seed)' })).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    expect(screen.getByRole('button', { name: 'package pkg/b' })).toHaveAttribute(
      'aria-pressed',
      'false',
    )
  })
})
