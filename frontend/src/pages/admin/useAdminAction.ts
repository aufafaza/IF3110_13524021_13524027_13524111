import { useState, type SubmitEvent } from 'react'
import { apiFetch } from '@/api/client'
import { useAuth } from '@/auth/useAuth'

export function useAdminAction() {
  const { state } = useAuth()
  const [busy, setBusy] = useState(false)
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)

  async function run(
    path: string, body?: object, options: {
      method?: 'POST' | 'PUT' | 'DELETE'
      message?: string
      onSuccess?: () => void
      reload?: boolean
    } = {},
  ) {
    if (state.status !== 'ready' || busy) return
    setBusy(true)
    setError('')
    setMessage('')
    try {
      await apiFetch(path, {
        method: options.method ?? 'POST',
        csrfToken: state.session.csrf_token,
        headers: body ? { 'Content-Type': 'application/json' } : undefined,
        body: body ? JSON.stringify(body) : undefined,
      })
      setMessage(options.message ?? 'Changes saved.')
      if (options.reload !== false) setRevision((value) => value + 1)
      options.onSuccess?.()
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Could not complete the request.')
    } finally {
      setBusy(false)
    }
  }

  return { run, busy, message, error, revision }
}

export function readForm(event: SubmitEvent<HTMLFormElement>) {
  event.preventDefault()
  return Object.fromEntries(new FormData(event.currentTarget))
}
