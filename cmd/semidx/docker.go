package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// dockerCompose is the minimum database semidx needs, mirroring the database
// half of deploy/docker-compose.yml: the pgvector image (the extension must
// exist on the server, not merely be requested), a healthcheck so dependants
// can wait for it, and a loopback-only port so the CLI on this machine can
// connect without exposing the database to the network.
const dockerCompose = `services:
  db:
    image: pgvector/pgvector:pg16
    environment:
      POSTGRES_USER: semidx
      POSTGRES_PASSWORD: ${SEMIDX_DB_PASSWORD:-changeme}
      POSTGRES_DB: semidx
    volumes:
      - semidx_db:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U semidx -d semidx"]
      interval: 5s
      timeout: 5s
      retries: 10
    ports:
      # Loopback only: reachable by semidx on this machine, never from the
      # network. Drop this block entirely when semidx runs in the same compose
      # project (it reaches the database as host "db"), as the reference
      # deployment in deploy/docker-compose.yml does.
      - "127.0.0.1:5432:5432"

volumes:
  semidx_db:
`

const dockerLong = `Print a ready-to-run PostgreSQL service for semidx, plus the connection string
and the checks that must pass.

semidx stores embeddings in PostgreSQL with pgvector. The database must provide:

  - the "vector" extension (pgvector), created by the migrations on first run;
  - the "pg_trgm" extension, used by keyword search;
  - pgvector 0.7 or newer IF you use an embedding model above 2000 dimensions
    (e.g. Gemini 3072): those are indexed through the halfvec operator class,
    which older pgvector releases do not ship;
  - a role allowed to run CREATE EXTENSION (the image's superuser qualifies).

A plain "postgres:16" image does NOT work: pgvector is not bundled with it.

After starting the service, verify it with:

  semidx doctor

which reports the server version, both extensions and halfvec support without
modifying anything.`

func newDockerCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "docker",
		Short:   "Print a ready-to-run PostgreSQL/pgvector service for semidx",
		Long:    dockerLong,
		Example: "  semidx docker > compose.yml && docker compose -f compose.yml up -d",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if _, err := fmt.Fprint(out, dockerCompose); err != nil {
				return err
			}
			_, err := fmt.Fprintf(out, `
# Save the block above as compose.yml, then:
#
#   SEMIDX_DB_PASSWORD=<a strong password> docker compose up -d
#   export SEMIDX_DB_DSN=postgres://semidx:<the same password>@localhost:5432/semidx?sslmode=disable
#   semidx doctor       # verifies pgvector, pg_trgm and halfvec support
#
# The full reference deployment (server + database) lives in
# deploy/docker-compose.yml; see docs/self-hosting.md.
`)
			return err
		},
	}
}
