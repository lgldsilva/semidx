import { useTheme, type Theme } from '../theme'

const NEXT: Record<Theme, Theme> = { light: 'dark', dark: 'system', system: 'light' }

function ThemeIcon({ theme }: Readonly<{ theme: Theme }>) {
  const common = {
    width: 16,
    height: 16,
    viewBox: '0 0 24 24',
    fill: 'none',
    stroke: 'currentColor',
    strokeWidth: 2,
    strokeLinecap: 'round',
    strokeLinejoin: 'round',
    'aria-hidden': true,
  } as const
  if (theme === 'light') {
    return (
      <svg {...common}>
        <circle cx="12" cy="12" r="4" />
        <path d="M12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4" />
      </svg>
    )
  }
  if (theme === 'dark') {
    return (
      <svg {...common}>
        <path d="M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8z" />
      </svg>
    )
  }
  return (
    <svg {...common}>
      <rect x="2" y="3" width="20" height="14" rx="2" />
      <path d="M8 21h8m-4-4v4" />
    </svg>
  )
}

/** Cycles light → dark → system. `system` follows the OS scheme. */
export function ThemeToggle() {
  const { theme, setTheme } = useTheme()
  const next = NEXT[theme]
  return (
    <button
      type="button"
      aria-label={`Theme: ${theme}. Switch to ${next}`}
      title={`Theme: ${theme} (click for ${next})`}
      className="inline-flex size-8 cursor-pointer items-center justify-center rounded-md border border-border bg-transparent p-0 text-fg hover:bg-surface-2"
      onClick={() => setTheme(next)}
    >
      <ThemeIcon theme={theme} />
    </button>
  )
}
