package webadmin

import "net/http"

// spaSettingsRedirect sends legacy server-rendered settings URLs to the React
// SPA settings route (keys/tokens/users/account tabs live there).
func spaSettingsRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
}
