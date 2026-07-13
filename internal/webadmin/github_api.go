package webadmin

import (
	"net/http"

	"github.com/lgldsilva/semidx/internal/github"
)

// apiGithubRepos lists the configured token owner's GitHub repositories so the
// admin UI can register a project from a repo without typing its clone URL. With
// no ?org=, it lists the authenticated user's repos; ?org=<name> lists that
// organization's repos. Admin-only: it exposes the token owner's private repos.
//
// The upstream error is logged but never returned verbatim — it may reference
// the token owner's account or org — mirroring the safe-error posture used for
// job failures.
func (a *Admin) apiGithubRepos(w http.ResponseWriter, r *http.Request, _ *authCtx) {
	if a.githubToken == "" {
		writeJSONErr(w, http.StatusConflict, "GitHub discovery is not configured — set SEMIDX_GITHUB_TOKEN on the server")
		return
	}
	var opts []github.Option
	if a.githubBaseURL != "" {
		opts = append(opts, github.WithBaseURL(a.githubBaseURL))
	}
	gh := github.New(a.githubToken, opts...)

	var (
		repos []github.Repo
		err   error
	)
	if org := r.URL.Query().Get("org"); org != "" {
		repos, err = gh.ListOrgRepos(r.Context(), org)
	} else {
		repos, err = gh.ListUserRepos(r.Context())
	}
	if err != nil {
		a.log.Warn("github repo discovery failed", "error", err)
		writeJSONErr(w, http.StatusBadGateway, "GitHub API request failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
}
