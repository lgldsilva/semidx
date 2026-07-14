import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, type SearchResponse } from '../api'
import { SearchPage } from './SearchPage'

function mockSearch(res: Partial<SearchResponse>) {
  vi.spyOn(api, 'projects').mockResolvedValue([])
  return vi.spyOn(api, 'search').mockResolvedValue({
    results: [],
    fallback: false,
    ...res,
  })
}

async function runQuery(query: string) {
  render(<SearchPage />)
  fireEvent.change(
    screen.getByPlaceholderText('where is authentication handled?'),
    { target: { value: query } },
  )
  fireEvent.click(screen.getByRole('button', { name: 'Search' }))
  // The submit round-trips through the mocked api.search promise.
  await screen.findByText('No matches.')
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe('SearchPage degraded badge', () => {
  it('shows the degraded alert with the retry hint rounded to seconds', async () => {
    mockSearch({ degraded: true, fallback: true, retry_after_ms: 2400 })
    await runQuery('anything')
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent(
      'Keyword results — embedding temporarily unavailable (try again in ~2s).',
    )
    // degraded replaces the plain keyword-fallback warning
    expect(
      screen.queryByText(/Keyword fallback — embeddings unavailable/),
    ).not.toBeInTheDocument()
  })

  it('omits the retry hint when the server sends no retry_after_ms', async () => {
    mockSearch({ degraded: true, fallback: true })
    await runQuery('anything')
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Keyword results — embedding temporarily unavailable.',
    )
  })

  it('keeps the existing keyword-fallback alert when only fallback=true', async () => {
    mockSearch({ fallback: true })
    await runQuery('anything')
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Keyword fallback — embeddings unavailable for this query.',
    )
    expect(screen.queryByText(/temporarily unavailable/)).not.toBeInTheDocument()
  })

  it('shows no alert on a healthy semantic search', async () => {
    mockSearch({})
    await runQuery('anything')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
