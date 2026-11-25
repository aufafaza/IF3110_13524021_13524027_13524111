import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { getMigration, listMigrations, migrationPost, type Migration } from '@/api/migrations'
import { useAuth } from '@/auth/useAuth'
import { useRequest } from '@/api/useRequest'
import { RequestFeedback } from '@/components/RequestFeedback'

export function MigrationsPage() {
  const { dbID, migrationID } = useParams()
  if (!dbID) return <h1>Database not specified</h1>

  const databasePath = `/databases/${dbID}`
  return migrationID ? (
    <>
      <Link className="back-link" to={`${databasePath}/migrations`}>Back to migrations</Link>
      <MigrationDetail key={`${dbID}/${migrationID}`} dbID={dbID} migrationID={migrationID} />
    </>
  ) : (
    <>
      <Link className="back-link" to={databasePath}>Back to tables</Link>
      <h1>Migrations</h1>
      <MigrationList dbID={dbID} />
    </>
  )
}

function MigrationList({ dbID }: { dbID: string }) {
  const request = useRequest(listMigrations, dbID)
  if (request.status !== 'ready') return <RequestFeedback request={request} loading="Loading migrations…" />

  return (
    <>
      {request.data.can_edit && (
        <Link className="back-link" to={`/databases/${dbID}/migrations/new`}>New migration</Link>
      )}
      {request.data.migrations.length === 0 ? <p className="feedback">No migrations available.</p> : (
        <ul className="resource-list" role="list">
          {request.data.migrations.map((migration) => (
            <li key={migration.id}>
              <Link className="resource-link migration-link" to={`/databases/${dbID}/migrations/${migration.id}`}>
                <span>{migration.title}</span>
                <span className="account-name">{migration.status}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </>
  )
}

function MigrationDetail({ dbID, migrationID }: { dbID: string; migrationID: string }) {
  const { state } = useAuth()
  const [result, setResult] = useState<Migration>()
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const request = useRequest(getMigration, dbID, migrationID)
  if (request.status !== 'ready') return <RequestFeedback request={request} loading="Loading migration…" />
  const migration = result ?? request.data

  async function execute() {
    if (running || state.status !== 'ready' || !window.confirm('Run this SQL on the selected database?')) return
    setRunning(true)
    setError('')
    try {
      setResult(await migrationPost<Migration>(dbID, migrationID, state.session.csrf_token, { confirm: true }, true))
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : 'Could not run the migration.')
      try {
        setResult(await getMigration(dbID, migrationID, new AbortController().signal))
      } catch {
        // Keep the execution error visible if the status refresh also fails.
      }
    } finally {
      setRunning(false)
    }
  }

  return (
    <>
      <h1>{migration.title}</h1>
      <dl className="migration-summary">
        <dt>Status</dt><dd>{migration.status}</dd>
        {migration.version != null && <><dt>Version</dt><dd>{migration.version}</dd></>}
        <dt>Created</dt><dd><time dateTime={migration.created_at}>{new Date(migration.created_at).toLocaleString()}</time></dd>
        {migration.executed_at && (
          <><dt>Executed</dt><dd><time dateTime={migration.executed_at}>{new Date(migration.executed_at).toLocaleString()}</time></dd></>
        )}
      </dl>
      {migration.error_message && <p className="feedback feedback-error">{migration.error_message}</p>}
      {error && <p className="feedback feedback-error" role="alert">{error}</p>}
      {request.data.can_edit && migration.status === 'draft' && (
        <div className="action-row">
          {!running && <Link to={`/databases/${dbID}/migrations/${migrationID}/edit`}>Edit draft</Link>}
          <button type="button" disabled={running} onClick={() => void execute()}>
            {running ? 'Running…' : 'Run migration'}
          </button>
        </div>
      )}
      <h2>SQL</h2>
      <pre className="sql-view" role="region" aria-label="Migration SQL" tabIndex={0}><code>{migration.sql}</code></pre>
    </>
  )
}
