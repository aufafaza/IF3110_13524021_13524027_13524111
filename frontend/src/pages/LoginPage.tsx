import { useState, type SubmitEvent } from 'react'
import { Navigate } from 'react-router-dom'
import { useAuth } from '@/auth/useAuth'
import { ApiError } from '@/api/client'

export function LoginPage() {
  const { state, login } = useAuth()
  const [loginError, setLoginError] = useState<string | null>(null)
  const ready = state.status === 'ready'

  if (ready && state.session.authenticated) {
    return <Navigate to="/databases" replace />
  }

  const handleSubmit = async (event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault()
    setLoginError(null)
    const formData = new FormData(event.currentTarget)
    try {
      await login(String(formData.get('username') ?? ''), String(formData.get('password') ?? ''))
    } catch (error) {
      setLoginError(error instanceof ApiError ? error.message : 'Could not sign in. Try again.')
    }
  }

  return (
    <main className="page">
      <h1>Sign in</h1>
      {/* TODO: Show session loading and bootstrap errors. Offer retry if loading fails. */}
      <form onSubmit={handleSubmit}>
        {loginError && <p role="alert">{loginError}</p>}
        <label>
          Username
          <input name="username" autoComplete="username" required />
        </label>
        <label>
          Password
          <input name="password" type="password" autoComplete="current-password" required />
        </label>
        <button type="submit" disabled={!ready}>Sign in</button>
      </form>
    </main>
  )
}
