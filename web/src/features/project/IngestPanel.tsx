import { useState } from 'react'
import { api, ApiError } from '../../api'
import { Alert } from '../../components/Alert'
import { Card } from '../../components/Card'
import { Code } from '../../components/Snippet'

const FILE_LABEL = 'block text-sm font-medium'
const FILE_INPUT = 'mt-1 block text-sm text-muted file:mr-2 file:cursor-pointer file:rounded-md file:border-0 file:bg-accent file:px-3 file:py-1.5 file:text-sm file:font-medium file:text-accent-fg'

export function IngestPanel({
  project,
  onDone,
}: {
  project: string
  onDone: () => void
}) {
  const [status, setStatus] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const maxFileBytes = 512 * 1024
  const batchSize = 50

  async function ingestFileList(list: FileList) {
    setBusy(true)
    setErr('')
    const all: { path: string; content: string }[] = []
    for (let i = 0; i < list.length; i++) {
      const f = list[i]
      const path =
        (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name
      if (f.size > maxFileBytes) {
        setErr((e) => `${e}\n${path}: skipped (>512KiB)`.trim())
        continue
      }
      const text = await f.text()
      all.push({ path: path.replace(/^\/+/, ''), content: text })
    }
    let indexed = 0
    let chunks = 0
    let errors = 0
    for (let i = 0; i < all.length; i += batchSize) {
      const batch = all.slice(i, i + batchSize)
      setStatus(`Indexing batch ${Math.floor(i / batchSize) + 1} (${batch.length} files)…`)
      const res = await api.projectIngest(project, batch)
      indexed += res.indexed
      chunks += res.chunks
      errors += res.errors
      if (res.file_errors?.length) {
        setErr(
          (prev) =>
            `${prev}\n${res.file_errors!
              .slice(0, 3)
              .map((x) => `${x.path}: ${x.error}`)
              .join('\n')}`.trim(),
        )
      }
    }
    setStatus(`Done — indexed ${indexed}, chunks ${chunks}, errors ${errors}`)
    onDone()
  }

  async function onPick(e: { target: HTMLInputElement }) {
    const list = e.target.files
    if (!list || list.length === 0) return
    setStatus(`Reading ${list.length} file(s)…`)
    try {
      await ingestFileList(list)
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'ingest failed')
    } finally {
      setBusy(false)
      e.target.value = ''
    }
  }

  async function onPickArchive(e: { target: HTMLInputElement }) {
    const list = e.target.files
    if (!list || list.length === 0) return
    const archive = list[0]
    setBusy(true)
    setErr('')
    setStatus(`Uploading archive ${archive.name}…`)
    try {
      const res = await api.projectIngestArchive(project, archive)
      setStatus(`Done — indexed ${res.indexed}, chunks ${res.chunks}, errors ${res.errors}`)
      if (res.file_errors?.length) {
        setErr(
          res.file_errors
            .slice(0, 10)
            .map((x) => `${x.path}: ${x.error}`)
            .join('\n'),
        )
      }
      onDone()
    } catch (ex) {
      setErr(ex instanceof ApiError ? ex.message : 'archive ingest failed')
    } finally {
      setBusy(false)
      e.target.value = ''
    }
  }

  return (
    <Card>
      <h2 className="mb-2 text-[1.1rem] font-bold">Ingest files</h2>
      <p className="m-0 text-muted">
        Upload text files, a whole folder, or a .zip archive (each file ≤512KiB). Large corpora:{' '}
        <Code>semidx push --project . --name {project}</Code>
      </p>
      {status && <Alert kind="success">{status}</Alert>}
      {err && (
        <Alert kind="error" className="whitespace-pre-wrap">
          {err}
        </Alert>
      )}
      <div className="mt-3 flex flex-wrap items-start gap-x-5 gap-y-2.5">
        <label className={FILE_LABEL}>
          Files
          <input
            type="file"
            className={FILE_INPUT}
            multiple
            disabled={busy}
            onChange={(e) => void onPick(e)}
          />
        </label>
        <label className={FILE_LABEL}>
          Folder
          <input
            type="file"
            className={FILE_INPUT}
            // @ts-expect-error webkitdirectory is non-standard but widely supported
            webkitdirectory=""
            directory=""
            multiple
            disabled={busy}
            onChange={(e) => void onPick(e)}
          />
        </label>
        <label className={FILE_LABEL}>
          .zip
          <input
            type="file"
            className={FILE_INPUT}
            accept=".zip,application/zip"
            disabled={busy}
            onChange={(e) => void onPickArchive(e)}
          />
        </label>
      </div>
    </Card>
  )
}
