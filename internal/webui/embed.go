// Package webui embeds the React admin SPA built from web/ into the semidx
// binary. Run `npm run build` in web/ (or scripts/build-web.sh) before
// `go build` so dist/ contains the production assets.
package webui

import "embed"

// Dist is the Vite production build (index.html + assets).
//
//go:embed all:dist
var Dist embed.FS
