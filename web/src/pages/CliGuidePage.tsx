import { useEffect, useState } from 'react'
import { api, type SystemInfo } from '../api'
import { Badge } from '../components/Badge'
import { Card } from '../components/Card'
import { Code, Snippet } from '../components/Snippet'

const H2 = 'mb-2 text-[1.1rem] font-bold'

export function CliGuidePage() {
  const [sys, setSys] = useState<SystemInfo | null>(null)

  useEffect(() => {
    void api.system().then(setSys).catch(() => undefined)
  }, [])

  return (
    <div>
      <h1 className="mb-1 text-[1.45rem] font-bold">CLI ↔ Server guide</h1>
      <p className="m-0 text-muted">
        One product, three surfaces: local CLI, remote CLI, and this admin UI.
        They share the same server index when you are logged in.
      </p>

      {sys && (
        <Card className="my-3.5">
          <h2 className={H2}>This server</h2>
          <p className="my-1">
            <Badge>{sys.mode}</Badge> {sys.storage}
          </p>
          <p className="my-1 text-muted">Capabilities: {sys.caps.join(', ')}</p>
        </Card>
      )}

      <Card className="my-3.5">
        <h2 className={H2}>Local machine (no server)</h2>
        <Snippet>{`semidx --local index --project .
semidx --local search --query "auth"
semidx --local status`}</Snippet>
      </Card>

      <Card className="my-3.5">
        <h2 className={H2}>Talk to this server from the CLI</h2>
        <Snippet>{`semidx login http://localhost:8080 --token <api-key>
semidx search --query "auth"          # remote
semidx push --project .               # send files to server
semidx index --to-server --project .  # same as push
semidx --local index --project .      # keep login, write local SQLite
semidx logout`}</Snippet>
        <p className="mt-2 mb-0 text-muted">
          Plain <Code>semidx index</Code> while logged in is refused on purpose —
          it used to write to the wrong place silently.
        </p>
      </Card>

      <Card className="my-3.5">
        <h2 className={H2}>What this UI can do today</h2>
        <ul className="my-2 list-disc pl-5">
          <li>List / create git projects / delete</li>
          <li>Queue reindex jobs and watch status</li>
          <li>Semantic search (one project or all)</li>
          <li>Ingest files, folders and .zip archives into push projects</li>
          <li>Project Analyze: callers, deps, explain, dead-code, SBOM, graph overview</li>
        </ul>
        <p className="mb-0 text-muted">
          For large repositories the CLI (<Code>semidx push</Code> or{' '}
          <Code>repo add</Code>) is still the fastest path. Diff, alerts, and insights
          remain CLI-only (stored locally under <Code>~/.config/semidx/</Code>).
        </p>
      </Card>

      <Card className="my-3.5">
        <h2 className={H2}>Advanced CLI commands</h2>
        <Snippet>{`semidx sbom generate --project myapp
semidx dead-code --project myapp
semidx diff --project myapp
semidx alerts check --project myapp
semidx insights show`}</Snippet>
      </Card>
    </div>
  )
}
