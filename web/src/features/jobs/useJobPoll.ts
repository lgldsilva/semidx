import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type Job } from '../../api'

const isTerminal = (status?: string) => status === 'succeeded' || status === 'failed'

/**
 * useJobPoll polls a single index job every 1.5s until it reaches a terminal
 * state, then fires onDone (e.g. to reload project data). Shared by the projects
 * list and the project workspace so the polling + terminal handling live in one
 * place. onDone is kept in a ref so an inline callback does not reset the timer.
 */
export function useJobPoll(onDone?: () => void) {
  const [job, setJob] = useState<Job | null>(null)
  const [project, setProject] = useState('')
  const doneRef = useRef(onDone)
  doneRef.current = onDone

  useEffect(() => {
    if (!job || !project || isTerminal(job.status)) return
    const t = setInterval(() => {
      void api
        .job(project, job.id)
        .then((j) => {
          setJob(j)
          if (isTerminal(j.status)) doneRef.current?.()
        })
        .catch(() => undefined)
    }, 1500)
    return () => clearInterval(t)
  }, [job, project])

  const start = useCallback((projectName: string, next: Job) => {
    setProject(projectName)
    setJob(next)
  }, [])

  return { job, project, start }
}
