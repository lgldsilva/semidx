import { fireEvent, render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
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

function renderSearch() {
  return render(
    <MemoryRouter>
      <SearchPage />
    </MemoryRouter>,
  )
}

async function runQuery(query: string) {
  renderSearch()
  fireEvent.change(
    screen.getByPlaceholderText('where is authentication handled?'),
    { target: { value: query } },
  )
  fireEvent.click(screen.getByRole('button', { name: 'Search' }))
  await screen.findByText('No matches')
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

  it('sends graph expansion when the toggle is on', async () => {
    const spy = mockSearch({})
    renderSearch()
    fireEvent.click(screen.getByLabelText('Expand via graph'))
    fireEvent.change(
      screen.getByPlaceholderText('where is authentication handled?'),
      { target: { value: 'auth' } },
    )
    fireEvent.click(screen.getByRole('button', { name: 'Search' }))
    await screen.findByText('No matches')
    expect(spy).toHaveBeenCalledWith(
      expect.objectContaining({ query: 'auth', graph: true, all: true }),
    )
  })

  it('runs from the URL without a project by searching all projects', async () => {
    const spy = mockSearch({})
    render(
      <MemoryRouter initialEntries={['/search?q=tokens']}>
        <SearchPage />
      </MemoryRouter>,
    )
    await screen.findByText('No matches')
    expect(spy).toHaveBeenCalledWith(
      expect.objectContaining({ query: 'tokens', all: true }),
    )
  })
})
