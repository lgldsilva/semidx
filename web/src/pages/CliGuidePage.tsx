import { useEffect, useState } from 'react'
import { api, type SystemInfo } from '../api'

export function CliGuidePage() {
  const [sys, setSys] = useState<SystemInfo | null>(null)

  useEffect(() => {
    void api.system().then(setSys).catch(() => undefined)
  }, [])

  return (
    <div>
      <h1>CLI ↔ Server guide</h1>
      <p className="muted">
        One product, three surfaces: local CLI, remote CLI, and this admin UI.
        They share the same server index when you are logged in.
      </p>

      {sys && (
        <div className="card">
          <h2>This server</h2>
          <p>
            <span className="pill">{sys.mode}</span> {sys.storage}
          </p>
          <p className="muted">Capabilities: {sys.caps.join(', ')}</p>
        </div>
      )}

      <div className="card">
        <h2>Local machine (no server)</h2>
        <pre className="snippet">{`semidx --local index --project .
semidx --local search --query "auth"
semidx --local status`}</pre>
      </div>

      <div className="card">
        <h2>Talk to this server from the CLI</h2>
        <pre className="snippet">{`semidx login http://localhost:8080 --token <api-key>
semidx search --query "auth"          # remote
semidx push --project .               # send files to server
semidx index --to-server --project .  # same as push
semidx --local index --project .      # keep login, write local SQLite
semidx logout`}</pre>
        <p className="muted">
          Plain <code>semidx index</code> while logged in is refused on purpose —
          it used to write to the wrong place silently.
        </p>
      </div>

      <div className="card">
        <h2>What this UI can do today</h2>
        <ul>
          <li>List / create git projects / delete</li>
          <li>Queue reindex jobs and watch status</li>
          <li>Semantic search (one project or all)</li>
        </ul>
        <p className="muted">
          Bulk file upload from the browser is not in this slice — use{' '}
          <code>semidx push</code> or <code>repo add</code>. More screens
          (keys, alerts, analyze) come next on the same SPA shell.
        </p>
      </div>
    </div>
  )
}
