import { type FormEvent, useEffect, useRef, useState } from 'react'
import ionLogoForDarkSurface from '../../../../assets/ion-logo-light/Wide620x300Logo.png'
import ionLogoForLightSurface from '../../../../assets/ion-logo-dark/Wide620x300Logo.png'

interface AuthStatus {
  required: boolean
  authenticated: boolean
}

export function AuthScreen() {
  const usernameRef = useRef<HTMLInputElement>(null)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [message, setMessage] = useState('')

  useEffect(() => {
    let active = true
    void fetch('/v1/auth/status', {
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    })
      .then(async (response) => {
        if (!response.ok) return
        const status = await response.json() as AuthStatus
        if (!active) return
        if (status.authenticated) {
          window.location.reload()
        } else if (!status.required) {
          setMessage('The local operator session is unavailable. Restart Ion and try again.')
        }
      })
      .catch(() => {
        if (active) setMessage('Ion is unavailable. Check the deployment and try again.')
      })
    return () => {
      active = false
    }
  }, [])

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    setSubmitting(true)
    setMessage('')
    try {
      const response = await fetch('/v1/auth/login', {
        method: 'POST',
        credentials: 'same-origin',
        headers: {
          Accept: 'application/json',
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ username, password }),
      })
      if (response.ok) {
        window.location.assign('/chat')
        return
      }
      const payload = await response.json().catch(() => undefined) as
        | { error?: string }
        | undefined
      setMessage(
        response.status === 429
          ? 'Too many attempts. Wait before trying again.'
          : (payload?.error ?? 'Username or password is incorrect.'),
      )
      setPassword('')
      usernameRef.current?.focus()
    } catch {
      setMessage('Ion is unavailable. Check your connection and try again.')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="auth-login">
      <section className="auth-login-panel" aria-labelledby="auth-title">
        <picture className="auth-login-logo">
          <source media="(prefers-color-scheme: dark)" srcSet={ionLogoForDarkSurface} />
          <img alt="Ion" src={ionLogoForLightSurface} />
        </picture>
        <div className="auth-login-heading">
          <p className="eyebrow">Private operator workspace</p>
          <h1 id="auth-title">Sign in to Ion</h1>
          <p>Use the credentials configured for this deployment.</p>
        </div>
        <form className="auth-login-form" onSubmit={(event) => { void submit(event) }}>
          <label htmlFor="ion-auth-username">
            Username
            <input
              autoCapitalize="none"
              autoComplete="username"
              autoCorrect="off"
              id="ion-auth-username"
              maxLength={128}
              onChange={(event) => setUsername(event.target.value)}
              ref={usernameRef}
              required
              spellCheck={false}
              type="text"
              value={username}
            />
          </label>
          <label htmlFor="ion-auth-password">
            Password
            <input
              autoComplete="current-password"
              id="ion-auth-password"
              maxLength={1024}
              onChange={(event) => setPassword(event.target.value)}
              required
              type="password"
              value={password}
            />
          </label>
          <button disabled={submitting} type="submit">
            {submitting ? 'Signing in…' : 'Sign in'}
          </button>
        </form>
        <p
          aria-live="polite"
          className="auth-login-message"
          data-visible={message !== ''}
          role="status"
        >
          {message}
        </p>
        <p className="auth-login-note">
          Your credentials are verified by this Ion deployment and are not sent to the agent.
        </p>
      </section>
    </main>
  )
}
