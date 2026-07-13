package mcpclient

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"charm.land/fantasy"
)

// ConnectAll connects to every enabled server in cfgs, aggregates their tools,
// and returns a closer that shuts down all the opened sessions.
//
// Failure policy — a single bad server never sinks the rest:
//   - Disabled servers (Enabled == false) are skipped silently.
//   - A server that fails to connect or to list its tools is logged (via the
//     default slog logger, server name + error only, no config fields so
//     secrets in Args/Env are not leaked) and skipped; the other servers still
//     contribute their tools.
//   - An error is returned only when NO session was established AND at least one
//     failure was a configuration error (invalid transport, missing command or
//     url). Pure runtime failures (server down, unreachable) never make
//     ConnectAll return an error — they are transient and only logged.
//
// The returned closer is always non-nil and safe to call regardless of the
// error; call it to release the sessions (it joins any per-session Close
// errors).
func ConnectAll(ctx context.Context, cfgs []ServerConfig) (tools []fantasy.AgentTool, closer func() error, err error) {
	var sessions []*Session
	closer = func() error {
		var errs []error
		for _, s := range sessions {
			if e := s.Close(); e != nil {
				errs = append(errs, e)
			}
		}
		return errors.Join(errs...)
	}

	var configErrs []error
	for _, cfg := range cfgs {
		if !cfg.Enabled {
			continue
		}
		toolset, sess, cfgErr := connectServerTools(ctx, cfg)
		if cfgErr != nil {
			if errors.Is(cfgErr, errInvalidConfig) {
				configErrs = append(configErrs, fmt.Errorf("mcp server %q: %w", cfg.Name, cfgErr))
			}
			continue
		}
		sessions = append(sessions, sess)
		tools = append(tools, toolset...)
	}

	if len(sessions) == 0 && len(configErrs) > 0 {
		return nil, closer, errors.Join(configErrs...)
	}
	return tools, closer, nil
}

func connectServerTools(ctx context.Context, cfg ServerConfig) ([]fantasy.AgentTool, *Session, error) {
	sess, err := Connect(ctx, cfg)
	if err != nil {
		slog.Warn("mcpclient: failed to connect to server", "server", cfg.Name, "error", err)
		return nil, nil, err
	}
	toolset, err := sess.Tools(ctx)
	if err != nil {
		slog.Warn("mcpclient: failed to list tools", "server", cfg.Name, "error", err)
		_ = sess.Close()
		return nil, nil, err
	}
	return toolset, sess, nil
}
