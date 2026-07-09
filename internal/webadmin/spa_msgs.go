package webadmin

// Shared SPA API / SSE user-facing strings (Sonar S1192).
const (
	spaErrProjectNotFound    = "project not found"
	spaErrInvalidJSONBody    = "invalid JSON body"
	spaErrInvalidJSON        = "invalid JSON"
	spaErrJobNotFound        = "job not found"
	spaErrFileTooLargeIngest = "file too large for browser ingest (max 512KiB)"
	// #nosec G101 -- SSE wire format string, not a credential.
	spaSSEDataFmt            = "data: %s\n\n"
)
