import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError } from '../api'
import { useChatPrefs } from './useChatPrefs'

afterEach(() => {
  vi.restoreAllMocks()
  window.localStorage.clear()
})

describe('useChatPrefs', () => {
  it('stays hidden when /chat/config 404s (older server)', async () => {
    const cfg = vi
      .spyOn(api, 'chatConfig')
      .mockRejectedValue(new ApiError(404, 'not found'))
    const { result } = renderHook(() => useChatPrefs())
    await waitFor(() => expect(cfg).toHaveBeenCalled())
    expect(result.current.enabled).toBe(false)
    expect(result.current.config).toBeNull()
  })

  it('stays hidden when the server reports enabled:false', async () => {
    vi.spyOn(api, 'chatConfig').mockResolvedValue({
      enabled: false,
      modes: ['rag'],
      models: ['m'],
    })
    const { result } = renderHook(() => useChatPrefs())
    await waitFor(() => expect(api.chatConfig).toHaveBeenCalled())
    expect(result.current.enabled).toBe(false)
  })

  it('exposes the config when enabled', async () => {
    vi.spyOn(api, 'chatConfig').mockResolvedValue({
      enabled: true,
      modes: ['rag', 'agent'],
      models: ['model-a'],
    })
    const { result } = renderHook(() => useChatPrefs())
    await waitFor(() => expect(result.current.enabled).toBe(true))
    expect(result.current.config?.modes).toEqual(['rag', 'agent'])
    expect(result.current.config?.models).toEqual(['model-a'])
  })

  it('persists the choice in localStorage and restores it on remount', async () => {
    vi.spyOn(api, 'chatConfig').mockResolvedValue({
      enabled: true,
      modes: ['rag', 'agent'],
      models: ['model-a', 'model-b'],
    })
    const first = renderHook(() => useChatPrefs())
    await waitFor(() => expect(first.result.current.enabled).toBe(true))
    act(() => {
      first.result.current.setMode('agent')
      first.result.current.setModel('model-b')
    })
    expect(window.localStorage.getItem('semidx.chat.mode')).toBe('agent')
    expect(window.localStorage.getItem('semidx.chat.model')).toBe('model-b')
    first.unmount()

    const second = renderHook(() => useChatPrefs())
    expect(second.result.current.mode).toBe('agent')
    expect(second.result.current.model).toBe('model-b')
  })

  it('clears the stored key when the choice returns to server default', async () => {
    vi.spyOn(api, 'chatConfig').mockResolvedValue({ enabled: true, modes: ['rag'] })
    const { result } = renderHook(() => useChatPrefs())
    await waitFor(() => expect(result.current.enabled).toBe(true))
    act(() => result.current.setMode('rag'))
    expect(window.localStorage.getItem('semidx.chat.mode')).toBe('rag')
    act(() => result.current.setMode(''))
    expect(window.localStorage.getItem('semidx.chat.mode')).toBeNull()
  })
})
