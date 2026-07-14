// Applies the persisted theme before React mounts so the page does not flash
// the wrong scheme. Ships as an external file because the admin CSP is
// script-src 'self' (inline scripts are blocked).
;(function () {
  try {
    var theme = localStorage.getItem('semidx.theme')
    if (theme === 'dark' || theme === 'light') {
      document.documentElement.dataset.theme = theme
    }
  } catch {
    // localStorage unavailable (privacy mode) — fall back to the OS scheme.
  }
})()
