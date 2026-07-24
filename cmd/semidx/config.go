package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/config"
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/xdg"
)

// newConfigCmd manages the persistent user config file (~/.config/semidx/semidx.env):
// embedding provider keys, the storage backend (Postgres DSN, local SQLite, or
// keyword-only), and server-only (serve) settings. It complements `semidx login`,
// which owns the remote-server connection (server_url/token). Together they let
// the CLI run standalone or against a server, with providers configured once.
func newConfigCmd(d *deps) *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Configure providers and the storage backend (SQLite, Postgres, or a remote server)",
		Long: "Manage semidx's persistent settings without hand-editing a .env.\n\n" +
			"Storage backends (run-time precedence for search: remote when logged in, unless --local / --backend local):\n" +
			"  • Remote server  — `semidx login <url> --token ...` (SEMIDX_BACKEND=remote|auto)\n" +
			"  • Local SQLite   — `semidx config set SEMIDX_LOCAL_INDEX 1` or `--local`\n" +
			"  • Postgres       — `semidx config set SEMIDX_DB_DSN postgres://...`\n\n" +
			"Note: `semidx index` always writes to a local store; use `semidx push` or\n" +
			"`semidx index --to-server` to send files to a logged-in server.\n\n" +
			"Values are stored in the user config file and layered below a project .env\n" +
			"and the real environment. Run `semidx config keys` for the full key reference.",
		Example: `  semidx config set GEMINI_API_KEY <key>
  semidx config set SEMIDX_DB_DSN postgres://localhost:5432/semidx
  semidx config list`,
	}
	c.AddCommand(
		newConfigSetCmd(),
		newConfigUnsetCmd(),
		newConfigGetCmd(),
		newConfigListCmd(d),
		newConfigKeysCmd(),
		newConfigPathCmd(),
	)
	return c
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "set <KEY> <VALUE>",
		Short:   "Persist a config value (e.g. GEMINI_API_KEY, SEMIDX_DB_DSN)",
		Example: "  semidx config set GEMINI_API_KEY AIza...",
		Args:    cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			if !isKnownKey(key) {
				fmt.Printf("[warn] %q is not a known semidx key — setting it anyway\n", key)
			}
			if err := config.SetUserEnv(key, value); err != nil {
				return err
			}
			p, _ := config.UserEnvPath()
			fmt.Printf("Set %s in %s\n", key, p)
			return nil
		},
	}
}

func newConfigUnsetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "unset <KEY>",
		Short:   "Remove a persisted config value",
		Example: "  semidx config unset GEMINI_API_KEY",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := config.UnsetUserEnv(args[0]); err != nil {
				return err
			}
			fmt.Printf("Unset %s\n", args[0])
			return nil
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "get <KEY>",
		Short:   "Print the effective value of a key (env > .env > user config)",
		Example: "  semidx config get SEMIDX_DB_DSN",
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			v := config.EffectiveValue(args[0])
			if v == "" {
				return fmt.Errorf("%s is not set", args[0])
			}
			fmt.Println(v)
			return nil
		},
	}
}

func newConfigListCmd(d *deps) *cobra.Command {
	var showSecrets bool
	c := &cobra.Command{
		Use:   "list",
		Short: "Show the effective configuration (secrets masked) and the active backend",
		Example: `  semidx config list
  semidx config list --show-secrets`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if p := xdg.Profile(); p != "" {
				fmt.Fprintf(out, "Profile: %s\n", p)
			}
			fmt.Fprintf(out, "Active backend: %s\n\n", activeBackend(d))
			fmt.Fprintln(out, "Settings (effective; env > .env > user config):")
			for _, k := range config.KnownKeys {
				v := config.EffectiveValue(k.Name)
				if v == "" {
					continue
				}
				fmt.Fprintf(out, "  %-24s %s\n", k.Name, displayValue(k.Name, v, showSecrets))
			}
			printOllamaRuntime(out, d)
			p, _ := config.UserEnvPath()
			fmt.Fprintf(out, "\nUser config file: %s\n", p)
			return nil
		},
	}
	c.Flags().BoolVar(&showSecrets, "show-secrets", false, "print secret values instead of masking them")
	return c
}

func newConfigKeysCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "keys",
		Short:   "List the configuration keys semidx understands",
		Example: "  semidx config keys",
		RunE: func(_ *cobra.Command, _ []string) error {
			for _, k := range config.KnownKeys {
				tag := ""
				if k.Secret {
					tag = " (secret)"
				}
				fmt.Printf("  %-24s %s%s\n", k.Name, k.Desc, tag)
			}
			return nil
		},
	}
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "path",
		Short:   "Print the user config file location",
		Long:    "Print the user config file location. When a profile is active, shows the profile name and the profile-specific config file.",
		Example: "  semidx config path\n  semidx --profile test config path",
		RunE: func(_ *cobra.Command, _ []string) error {
			if p := xdg.Profile(); p != "" {
				fmt.Printf("Profile: %s\n", p)
			}
			path, err := config.UserEnvPath()
			if err != nil {
				return err
			}
			fmt.Println(path)
			return nil
		},
	}
}

// activeBackend reports which storage backend the CLI will use, honoring the
// remote (when useRemote) > Postgres (configured) > SQLite > Postgres (default)
// precedence. When a login is saved but this run forced local, that is noted.
func activeBackend(d *deps) string {
	if d != nil && d.remote() {
		return "remote server (" + d.client.ServerURL + ")"
	}

	// Resolve the active local index path. When deps is fully initialized,
	// PersistentPreRunE populates d.localIndexPath; otherwise fall back to the
	// loaded config or environment.
	var localPath string
	switch {
	case d != nil && d.localIndexPath != "":
		localPath = d.localIndexPath
	case d != nil && d.cfg != nil && d.cfg.LocalIndexPath != "":
		localPath = d.cfg.LocalIndexPath
	default:
		// Fallback for when configuration is evaluated statically (e.g. in config commands or tests).
		if config.EffectiveValue("SEMIDX_DB_DSN") == "" {
			localPath = config.EffectiveValue("SEMIDX_LOCAL_INDEX")
		}
	}

	// Resolve keyword-only mode. When deps is fully initialized, PersistentPreRunE
	// populates d.keywordOnly; otherwise fall back to the environment.
	keywordOnly := false
	switch {
	case d != nil && d.keywordOnly:
		keywordOnly = true
	case config.EffectiveValue("SEMIDX_EMBED_MODE") == "none":
		keywordOnly = true
	}

	var label string
	switch {
	case keywordOnly && localPath != "":
		label = "local SQLite, keyword-only"
	case keywordOnly:
		label = "keyword-only"
	case localPath != "":
		label = "local SQLite (" + localPath + ")"
	case config.EffectiveValue("SEMIDX_DB_DSN") != "":
		label = "Postgres (" + mask(config.EffectiveValue("SEMIDX_DB_DSN")) + ")"
	default:
		label = "Postgres (default localhost DSN — set SEMIDX_DB_DSN, or use --local)"
	}
	if d != nil && d.hasServerConfig() && !d.remote() {
		label += " (saved login " + d.client.ServerURL + " ignored; --local/--backend=local)"
	}
	return label
}

func isKnownKey(key string) bool {
	for _, k := range config.KnownKeys {
		if k.Name == key {
			return true
		}
	}
	return false
}

// displayValue masks secret values unless showSecrets is set.
func displayValue(key, value string, showSecrets bool) string {
	if showSecrets || !config.IsSecret(key) {
		return value
	}
	return mask(value)
}

// mask reduces a secret to a hint (last 4 chars) so listings never leak it.
func mask(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

// printOllamaRuntime soft-probes configured local Ollama URLs (GET /api/ps) and
// reports GPU vs CPU from size_vram. Never fails the parent command.
func printOllamaRuntime(out io.Writer, d *deps) {
	if d != nil && d.keywordOnly {
		fmt.Fprintln(out, "\nOllama runtime: skipped (keyword-only mode)")
		return
	}
	urls := ollamaProbeURLs(d)
	if len(urls) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	fmt.Fprintln(out, "\nOllama runtime (local; GPU via size_vram, not nvidia-smi):")
	for _, p := range embed.ProbeOllamaRuntimes(ctx, urls) {
		fmt.Fprintf(out, "  %-40s %s\n", p.URL, p.Summary())
	}
}

func ollamaProbeURLs(d *deps) []string {
	var url string
	var urls []string
	if d != nil && d.cfg != nil {
		url = d.cfg.OllamaURL
		urls = d.cfg.OllamaURLs
	} else {
		url = config.EffectiveValue("SEMIDX_OLLAMA_URL")
		if url == "" {
			url = config.EffectiveValue("OLLAMA_URL")
		}
	}
	out := embed.OllamaProbeURLs(url, urls)
	if len(out) == 0 {
		return []string{"http://localhost:11434"}
	}
	return out
}
