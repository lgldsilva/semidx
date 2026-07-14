import { fireEvent, render, screen } from '@testing-library/react'
import { beforeEach, describe, expect, it } from 'vitest'
import { ThemeToggle } from './components/ThemeToggle'
import { ThemeProvider, useTheme } from './theme'

function ThemeSpy() {
  const { theme } = useTheme()
  return <span data-testid="theme">{theme}</span>
}

function renderThemed() {
  return render(
    <ThemeProvider>
      <ThemeSpy />
      <ThemeToggle />
    </ThemeProvider>,
  )
}

beforeEach(() => {
  window.localStorage.clear()
  delete document.documentElement.dataset.theme
})

describe('ThemeProvider', () => {
  it('defaults to system when the DOM has no data-theme', () => {
    renderThemed()
    expect(screen.getByTestId('theme')).toHaveTextContent('system')
    expect(document.documentElement.dataset.theme).toBeUndefined()
  })

  it('reads the initial theme stamped on the DOM by theme-init.js', () => {
    document.documentElement.dataset.theme = 'dark'
    renderThemed()
    expect(screen.getByTestId('theme')).toHaveTextContent('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')
  })

  it('ignores unexpected data-theme values', () => {
    document.documentElement.dataset.theme = 'purple'
    renderThemed()
    expect(screen.getByTestId('theme')).toHaveTextContent('system')
  })

  it('throws when useTheme is used outside the provider', () => {
    expect(() => render(<ThemeSpy />)).toThrow(/ThemeProvider/)
  })
})

describe('ThemeToggle', () => {
  it('cycles system → light → dark → system, applying data-theme', () => {
    renderThemed()
    const btn = screen.getByRole('button', { name: /switch to light/i })

    fireEvent.click(btn)
    expect(screen.getByTestId('theme')).toHaveTextContent('light')
    expect(document.documentElement.dataset.theme).toBe('light')

    fireEvent.click(screen.getByRole('button', { name: /switch to dark/i }))
    expect(screen.getByTestId('theme')).toHaveTextContent('dark')
    expect(document.documentElement.dataset.theme).toBe('dark')

    fireEvent.click(screen.getByRole('button', { name: /switch to system/i }))
    expect(screen.getByTestId('theme')).toHaveTextContent('system')
    expect(document.documentElement.dataset.theme).toBeUndefined()
  })

  it('persists explicit themes and clears the key for system', () => {
    renderThemed()

    fireEvent.click(screen.getByRole('button', { name: /switch to light/i }))
    expect(window.localStorage.getItem('semidx.theme')).toBe('light')

    fireEvent.click(screen.getByRole('button', { name: /switch to dark/i }))
    expect(window.localStorage.getItem('semidx.theme')).toBe('dark')

    fireEvent.click(screen.getByRole('button', { name: /switch to system/i }))
    expect(window.localStorage.getItem('semidx.theme')).toBeNull()
  })
})
