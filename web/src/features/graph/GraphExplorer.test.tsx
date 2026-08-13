import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api'
import { GraphExplorer } from './GraphExplorer'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('GraphExplorer', () => {
  it('refetches hubs when the project changes', async () => {
    vi.spyOn(api, 'projectGraphSubgraph').mockResolvedValue({ nodes: [], edges: [] })
    const stats = vi.spyOn(api, 'projectGraphStats')
    stats
      .mockResolvedValueOnce({
        nodes: 1,
        edges: 0,
        top_depends: [],
        top_depended: [{ node: 'old.go', degree: 3 }],
      })
      .mockResolvedValueOnce({
        nodes: 1,
        edges: 0,
        top_depends: [],
        top_depended: [{ node: 'new.go', degree: 4 }],
      })
    const { rerender } = render(
      <GraphExplorer project="alpha" seedPath="" onOpenFile={() => undefined} onAsk={() => undefined} />,
    )
    expect(await screen.findByText('old.go')).toBeInTheDocument()
    rerender(
      <GraphExplorer project="beta" seedPath="" onOpenFile={() => undefined} onAsk={() => undefined} />,
    )
    expect(await screen.findByText('new.go')).toBeInTheDocument()
    expect(stats).toHaveBeenCalledTimes(2)
  })
})
