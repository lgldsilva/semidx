import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'

export type Theme = 'light' | 'dark' | 'system'

const STORAGE_KEY = 'semidx.theme'

interface ThemeContextValue {
  theme: Theme
  setTheme: (theme: Theme) => void
}

const ThemeContext = createContext<ThemeContextValue | null>(null)

// theme-init.js already stamped <html data-theme> before React mounted, so the
// DOM (not localStorage) is the source of truth for the initial render.
function readInitialTheme(): Theme {
  const t = document.documentElement.dataset.theme
  return t === 'light' || t === 'dark' ? t : 'system'
}

export function ThemeProvider({ children }: Readonly<{ children: React.ReactNode }>) {
  const [theme, setThemeState] = useState<Theme>(readInitialTheme)

  useEffect(() => {
    const root = document.documentElement
    if (theme === 'system') {
      delete root.dataset.theme
    } else {
      root.dataset.theme = theme
    }
  }, [theme])

  // Persist only on user-initiated changes, never on mount: if theme-init.js
  // failed to load (404/transient error), readInitialTheme() sees 'system' and
  // a mount-time write would silently wipe the stored preference instead of
  // merely not applying it for this page load.
  const setTheme = useCallback((t: Theme) => {
    setThemeState(t)
    try {
      if (t === 'system') {
        window.localStorage.removeItem(STORAGE_KEY)
      } else {
        window.localStorage.setItem(STORAGE_KEY, t)
      }
    } catch {
      // localStorage unavailable (privacy mode) — the theme still applies,
      // it just will not survive a reload.
    }
  }, [])

  const value = useMemo(() => ({ theme, setTheme }), [theme, setTheme])
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext)
  if (!ctx) throw new Error('useTheme must be used within a ThemeProvider')
  return ctx
}
