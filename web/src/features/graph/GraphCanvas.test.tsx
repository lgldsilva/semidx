import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
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
