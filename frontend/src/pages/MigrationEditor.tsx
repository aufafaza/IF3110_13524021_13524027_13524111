import { useState, type SubmitEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { getMigration, listMigrations, migrationPost, type Migration, type Preview } from '@/api/migrations'
import { useRequest } from '@/api/useRequest'
import { useAuth } from '@/auth/useAuth'
import { RequestFeedback } from '@/components/RequestFeedback'

export function NewMigrationPage() {
  const { dbID } = useParams()
  return dbID ? <NewMigration key={dbID} dbID={dbID} /> : null
}

function NewMigration({ dbID }: { dbID: string }) {
  const request = useRequest(listMigrations, dbID)
  return (
    <>
      <Link className="back-link" to={`/databases/${dbID}/migrations`}>Back to migrations</Link>
      <h1>New migration</h1>
      {request.status !== 'ready' ? <RequestFeedback request={request} loading="Loading permissions…" /> :
        request.data.can_edit ? <Editor dbID={dbID} /> : <p role="alert">Edit access is required.</p>}
    </>
  )
}

export function EditMigrationPage() {
  const { dbID, migrationID } = useParams()
  return dbID && migrationID ? <EditMigration key={`${dbID}/${migrationID}`} dbID={dbID} migrationID={migrationID} /> : null
}

function EditMigration({ dbID, migrationID }: { dbID: string; migrationID: string }) {
  const request = useRequest(getMigration, dbID, migrationID)
  return (
    <>
      <Link className="back-link" to={`/databases/${dbID}/migrations/${migrationID}`}>Back to migration</Link>
      <h1>Edit draft</h1>
      {request.status !== 'ready' ? <RequestFeedback request={request} loading="Loading draft…" /> :
        request.data.can_edit && request.data.status === 'draft'
          ? <Editor dbID={dbID} draft={request.data} />
          : <p role="alert">This migration cannot be edited.</p>}
    </>
  )
}

function Editor({ dbID, draft }: { dbID: string; draft?: Migration }) {
  const { state } = useAuth()
  const navigate = useNavigate()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [preview, setPreview] = useState<Preview>()

  async function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy || state.status !== 'ready') return
    const values = new FormData(event.currentTarget)
    const action = event.nativeEvent.submitter?.getAttribute('value') ?? 'save'
    const input = { title: String(values.get('title') ?? ''), sql: String(values.get('sql') ?? ''), action }
    const migrationID = draft ? String(draft.id) : undefined
    setBusy(true)
    setError('')
    setPreview(undefined)
    try {
      if (action === 'save') {
        const result = await migrationPost<Migration>(dbID, migrationID, state.session.csrf_token, input)
        navigate(`/databases/${dbID}/migrations/${result.id}`)
      } else {
        setPreview(await migrationPost<Preview>(dbID, migrationID, state.session.csrf_token, input))
      }
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Could not process the migration.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <form className="editor-form" onSubmit={submit} onChange={() => setPreview(undefined)}>
        <label>Title<input name="title" required defaultValue={draft?.title} disabled={busy} /></label>
        <label>SQL<textarea name="sql" rows={12} spellCheck={false} required defaultValue={draft?.sql} disabled={busy} /></label>
        <div className="action-row">
          <button value="save" disabled={busy}>Save draft</button>
          <button className="button-secondary" value="preview" disabled={busy}>Preview</button>
          <button className="button-secondary" value="explain" disabled={busy}>Explain</button>
          <button className="button-secondary" value="explain_analyze" disabled={busy}>Explain analyze</button>
        </div>
      </form>
      {busy && <p role="status">Working…</p>}
      {error && <p className="feedback feedback-error" role="alert">{error}</p>}
      {preview && (
        <section>
          <h2>Preview</h2>
          <p>{preview.statement_count} SQL statement(s)</p>
          <pre className="sql-view" tabIndex={0}>{preview.statements.join('\n\n')}</pre>
          {preview.plan_report?.statements.map((statement) => (
            <pre className="sql-view" key={statement.index} tabIndex={0}>
              {statement.is_explainable ? statement.raw_plan : 'An execution plan is unavailable for this statement.'}
            </pre>
          ))}
        </section>
      )}
    </>
  )
}
