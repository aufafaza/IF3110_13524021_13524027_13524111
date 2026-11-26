import { useState, type SubmitEvent } from 'react'
import { Navigate } from 'react-router-dom'
import { useAuth } from '@/auth/useAuth'
import { ApiError } from '@/api/client'

export function LoginPage() {
  const { state, login, refreshSession } = useAuth()
  const [loginError, setLoginError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const ready = state.status === 'ready'

  if (ready && state.session.authenticated) {
    return <Navigate to="/databases" replace />
  }

  const handleSubmit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!ready || submitting) return
    setLoginError(null)
    setSubmitting(true)
    const formData = new FormData(event.currentTarget)
    try {
      await login(String(formData.get('username') ?? ''), String(formData.get('password') ?? ''))
    } catch (error) {
      setLoginError(error instanceof ApiError ? error.message : 'Could not sign in. Try again.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="login-page">
      <section className="login-panel" aria-labelledby="login-title">
        <h1 id="login-title">Sign in</h1>
        {state.status === 'loading' && <p className="feedback" role="status">Loading session…</p>}
        {state.status === 'error' && (
          <>
            <p className="feedback feedback-error" role="alert">{state.message}</p>
            <button className="button-secondary" type="button" onClick={() => void refreshSession()}>Retry</button>
          </>
        )}
        {ready && (
          <form className="login-form" onSubmit={handleSubmit}>
            {loginError && <p className="feedback feedback-error" role="alert">{loginError}</p>}
            <label>
              Username
              <input name="username" autoComplete="username" required />
            </label>
            <label>
              Password
              <input name="password" type="password" autoComplete="current-password" required />
            </label>
            <button type="submit" disabled={submitting}>{submitting ? 'Signing in…' : 'Sign in'}</button>
          </form>
        )}
      </section>
    </main>
  )
}
