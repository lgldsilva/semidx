package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/config"
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
			"Storage backends (choose one; run-time precedence is remote > SQLite > Postgres):\n" +
			"  • Remote server  — `semidx login <url> --token ...`\n" +
			"  • Local SQLite   — `semidx config set SEMIDX_LOCAL_INDEX 1` (or a path)\n" +
			"  • Postgres       — `semidx config set SEMIDX_DB_DSN postgres://...`\n\n" +
			"Values are stored in the user config file and layered below a project .env\n" +
			"and the real environment. Run `semidx config keys` for the full key reference.",
		Example: `  semidx config set GEMINI_API_KEY <key>
  semidx config set SEMIDX_DB_DSN postgres://user:pass@host:5432/db
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
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Printf("Active backend: %s\n\n", activeBackend(d))
			fmt.Println("Settings (effective; env > .env > user config):")
			for _, k := range config.KnownKeys {
				v := config.EffectiveValue(k.Name)
				if v == "" {
					continue
				}
				fmt.Printf("  %-24s %s\n", k.Name, displayValue(k.Name, v, showSecrets))
			}
			p, _ := config.UserEnvPath()
			fmt.Printf("\nUser config file: %s\n", p)
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
		Example: "  semidx config path",
		RunE: func(_ *cobra.Command, _ []string) error {
			p, err := config.UserEnvPath()
			if err != nil {
				return err
			}
			fmt.Println(p)
			return nil
		},
	}
}

// activeBackend reports which storage backend the CLI will use, honoring the
// remote > SQLite > Postgres precedence that indexStore/remote() implement.
func activeBackend(d *deps) string {
	if d != nil && d.remote() {
		return "remote server (" + d.client.ServerURL + ")"
	}
	if config.EffectiveValue("SEMIDX_EMBED_MODE") == "none" {
		if p := config.EffectiveValue("SEMIDX_LOCAL_INDEX"); p != "" {
			return "local SQLite, keyword-only"
		}
		return "keyword-only"
	}
	if p := config.EffectiveValue("SEMIDX_LOCAL_INDEX"); p != "" {
		return "local SQLite (" + p + ")"
	}
	if dsn := config.EffectiveValue("SEMIDX_DB_DSN"); dsn != "" {
		return "Postgres (" + mask(dsn) + ")"
	}
	return "Postgres (default localhost DSN — set SEMIDX_DB_DSN, or use --local)"
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
