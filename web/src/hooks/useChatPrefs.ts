import { useCallback, useEffect, useState } from 'react'
import { api, type ChatConfig } from '../api'

const MODE_KEY = 'semidx.chat.mode'
const MODEL_KEY = 'semidx.chat.model'

function readStored(key: string): string {
  try {
    return window.localStorage.getItem(key) ?? ''
  } catch {
    return ''
  }
}

function writeStored(key: string, value: string) {
  try {
    if (value) window.localStorage.setItem(key, value)
    else window.localStorage.removeItem(key)
  } catch {
    /* private mode / quota: preference just won't persist */
  }
}

/**
 * useChatPrefs drives the chat mode/model selector. It loads
 * GET /admin/api/chat/config — a 404 (older server) or enabled:false keeps
 * `enabled` false so the selector stays hidden — and persists the user's
 * choice in localStorage. Empty mode/model mean "server default" (not sent).
 */
export function useChatPrefs() {
  const [config, setConfig] = useState<ChatConfig | null>(null)
  const [mode, setModeState] = useState(() => readStored(MODE_KEY))
  const [model, setModelState] = useState(() => readStored(MODEL_KEY))

  useEffect(() => {
    let alive = true
    api
      .chatConfig()
      .then((c) => {
        if (alive) setConfig(c.enabled ? c : null)
      })
      .catch(() => {
        // 404 / network / old server: hide the selector.
        if (alive) setConfig(null)
      })
    return () => {
      alive = false
    }
  }, [])

  const setMode = useCallback((m: string) => {
    setModeState(m)
    writeStored(MODE_KEY, m)
  }, [])

  const setModel = useCallback((m: string) => {
    setModelState(m)
    writeStored(MODEL_KEY, m)
  }, [])

  return { enabled: config !== null, config, mode, model, setMode, setModel }
}
