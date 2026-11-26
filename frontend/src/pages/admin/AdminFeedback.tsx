export function AdminFeedback({ error, message, busy }: { error: string; message: string; busy: boolean }) {
  return (
    <>
      {busy && <p className="feedback" role="status">Working…</p>}
      {error && <p className="feedback feedback-error" role="alert">{error}</p>}
      {message && <p className="feedback" role="status">{message}</p>}
    </>
  )
}
