import { useState, type SubmitEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { getMigration, listMigrations, migrationPost, type BatchConfig, type Migration, type Preview } from '@/api/migrations'
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
        request.data.can_edit && request.data.status === 'draft' && !request.data.first_executed_at
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
  const [reversible, setReversible] = useState(!draft || !!draft.down_sql)
  const [downSQL, setDownSQL] = useState(draft?.down_sql ?? '')
  const [reason, setReason] = useState(draft?.irreversible_reason ?? '')
  const [batched, setBatched] = useState(!!draft?.batch_config)
  const [saveSnapshots, setSaveSnapshots] = useState(!!draft?.batch_config?.recovery)

  async function submit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy || state.status !== 'ready') return
    const values = new FormData(event.currentTarget)
    const action = event.nativeEvent.submitter?.getAttribute('value') ?? 'save'
    const input = { title: String(values.get('title') ?? ''), sql: String(values.get('sql') ?? ''), action }
    if (action === 'save' && !(reversible ? downSQL.trim() : reason.trim())) {
      setError(reversible ? 'Enter down SQL for this reversible migration.' : 'Explain why this migration is irreversible.')
      return
    }
    const migrationID = draft ? String(draft.id) : undefined
    setBusy(true)
    setError('')
    setPreview(undefined)
    try {
      if (action === 'save') {
        const definition = reversible ? { down_sql: downSQL } : { irreversible_reason: reason.trim() }
        let batchConfig: BatchConfig | undefined
        if (batched) {
          const scope = String(values.get('scope_sql') ?? '').trim()
          const size = Number(values.get('batch_size'))
          if (!scope || !Number.isInteger(size) || size < 1 || size > 10000) {
            setError('Enter scope SQL and a batch size between 1 and 10,000.')
            return
          }
          batchConfig = { scope_sql: scope, size }
          if (reversible && saveSnapshots) {
            const schema = String(values.get('recovery_schema') ?? '').trim()
            const table = String(values.get('recovery_table') ?? '').trim()
            const key = String(values.get('recovery_key') ?? '').trim()
            if (!schema || !table || !key) {
              setError('Enter the schema, table, and key to record compensation snapshots.')
              return
            }
            batchConfig.recovery = { schema, table, key }
          }
        }
        const result = await migrationPost<Migration>(dbID, migrationID, state.session.csrf_token, {
          ...input, ...definition, mode: batched ? 'batch' : 'ordinary', batch_config: batchConfig,
        })
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
        <label>Up SQL<textarea name="sql" rows={12} spellCheck={false} required defaultValue={draft?.sql} disabled={busy} /></label>
        <label>
          Reversibility
          <select value={reversible ? 'reversible' : 'irreversible'} disabled={busy}
            onChange={(event) => setReversible(event.target.value === 'reversible')}>
            <option value="reversible">Reversible</option>
            <option value="irreversible">Irreversible</option>
          </select>
        </label>
        {reversible ? (
          <label>Down SQL<textarea rows={6} spellCheck={false} value={downSQL} disabled={busy}
            onChange={(event) => setDownSQL(event.target.value)} /></label>
        ) : (
          <label>Reason<input value={reason} disabled={busy} onChange={(event) => setReason(event.target.value)} /></label>
        )}
        <label>
          Execution mode
          <select value={batched ? 'batch' : 'ordinary'} disabled={busy}
            onChange={(event) => setBatched(event.target.value === 'batch')}>
            <option value="ordinary">Ordinary</option>
            <option value="batch">Batched</option>
          </select>
        </label>
        {batched && (
          <>
            <p>Up SQL and down SQL each use one UPDATE with the batch’s bigint[] keys in <code>$1</code>.
              Return the updated key for every row.</p>
            <label>Scope SQL
              <textarea name="scope_sql" rows={4} spellCheck={false} disabled={busy} defaultValue={draft?.batch_config?.scope_sql}
                placeholder="SELECT id AS key FROM public.orders WHERE needs_update" />
            </label>
            <label>Rows per batch
              <input name="batch_size" type="number" min={1} max={10000} step={1} disabled={busy}
                defaultValue={draft?.batch_config?.size ?? 1000} />
            </label>
            {reversible && (
              <>
                <label className="checkbox-label">
                  <input type="checkbox" checked={saveSnapshots} disabled={busy}
                    onChange={(event) => setSaveSnapshots(event.target.checked)} />
                  Record row snapshots for compensation
                </label>
                {saveSnapshots && (
                  <>
                    <label>Schema<input name="recovery_schema" defaultValue={draft?.batch_config?.recovery?.schema ?? 'public'} /></label>
                    <label>Table<input name="recovery_table" defaultValue={draft?.batch_config?.recovery?.table} /></label>
                    <label>Key column<input name="recovery_key" defaultValue={draft?.batch_config?.recovery?.key ?? 'id'} /></label>
                  </>
                )}
              </>
            )}
          </>
        )}
        <div className="action-row">
          <button value="save" disabled={busy}>Save draft</button>
          <button className="button-secondary" value="preview" disabled={busy}>Preview</button>
          {!batched && (
            <>
              <button className="button-secondary" value="explain" disabled={busy}>Explain</button>
              <button className="button-secondary" value="explain_analyze" disabled={busy}>Explain analyze</button>
            </>
          )}
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
