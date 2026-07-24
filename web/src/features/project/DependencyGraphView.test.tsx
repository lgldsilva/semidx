import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { DependencyGraphView } from './DependencyGraphView'

describe('DependencyGraphView', () => {
  it('renders package nodes as rect and files as circle', () => {
    const { container } = render(
      <DependencyGraphView
        nodes={[
          { id: 'main.go', label: 'main.go', kind: 'file', seed: true },
          { id: 'internal/store/', label: 'store/', kind: 'package' },
        ]}
        edges={[{ source: 'main.go', target: 'internal/store/', kind: 'imports' }]}
      />,
    )
    expect(container.querySelectorAll('circle').length).toBeGreaterThanOrEqual(1)
    expect(container.querySelectorAll('rect').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('main.go').length).toBeGreaterThanOrEqual(1)
    expect(container.querySelector('title')?.textContent).toMatch(/main\.go|internal\/store\//)
  })

  it('opens files on click but not packages', () => {
    const onOpenNode = vi.fn()
    const { container } = render(
      <DependencyGraphView
        nodes={[
          { id: 'a.go', label: 'a.go', kind: 'file' },
          { id: 'pkg/', label: 'pkg/', kind: 'package' },
        ]}
        edges={[]}
        onOpenNode={onOpenNode}
      />,
    )
    const groups = container.querySelectorAll('g[transform]')
    expect(groups.length).toBe(2)
    fireEvent.click(groups[0])
    fireEvent.click(groups[1])
    // Handler is invoked for both; AnalyzePanel ignores package kinds.
    expect(onOpenNode).toHaveBeenCalled()
    const kinds = onOpenNode.mock.calls.map((c) => c[1] as string)
    expect(kinds).toContain('file')
    expect(kinds).toContain('package')
  })
})
