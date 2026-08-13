import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { SearchHits } from './SearchHits'

describe('SearchHits', () => {
  it('links a hit to the file, graph, and chat surfaces', () => {
    render(
      <MemoryRouter>
        <SearchHits
          fallbackProject="demo"
          results={[
            {
              path: 'internal/auth/token.go',
              start_line: 12,
              end_line: 40,
              score: 0.82,
              content: 'func Validate()',
              source: 'graph',
              graph_depth: 2,
              stale: true,
              confidence: 'EXTRACTED',
              symbol: 'Validate',
            },
          ]}
        />
      </MemoryRouter>,
    )
    expect(screen.getByText(/internal\/auth\/token.go:12-40/)).toBeInTheDocument()
    expect(screen.getByText('graph d=2')).toBeInTheDocument()
    expect(screen.getByText('stale')).toBeInTheDocument()
    expect(screen.getByText(/EXTRACTED Validate/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open file' })).toHaveAttribute(
      'href',
      '/projects/demo?tab=files&path=internal%2Fauth%2Ftoken.go&line=12',
    )
    expect(screen.getByRole('link', { name: 'View in graph' })).toHaveAttribute(
      'href',
      '/projects/demo?tab=graph&path=internal%2Fauth%2Ftoken.go',
    )
    expect(screen.getByRole('link', { name: 'Ask about this' })).toHaveAttribute(
      'href',
      expect.stringContaining('tab=chat'),
    )
    expect(screen.getByRole('link', { name: 'Ask about this' }).getAttribute('href')).not.toContain('func%20Validate')
  })
})
