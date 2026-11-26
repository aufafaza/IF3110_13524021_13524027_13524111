import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { getMigration, listAttemptHistory, listMigrations, operationLabels, type Migration } from '@/api/migrations'
import { useRequest } from '@/api/useRequest'
import { RequestFeedback } from '@/components/RequestFeedback'
import { MigrationWorkflow } from './MigrationWorkflow'

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
                <span className="account-name">{migration.status.replaceAll('_', ' ')}</span>
              </Link>
            </li>
          ))}
        </ul>
      )}
      <MigrationHistory dbID={dbID} migrations={request.data.migrations} />
    </>
  )
}

function MigrationHistory({ dbID, migrations }: { dbID: string; migrations: Migration[] }) {
  const [hideRollback, setHideRollback] = useState(false)
  const request = useRequest(listAttemptHistory, dbID, String(hideRollback))
  const names = new Map(migrations.map((migration) => [migration.id, migration.title]))

  return (
    <section aria-labelledby="attempt-history">
      <h2 id="attempt-history">Execution history</h2>
      <label className="action-row">
        <input type="checkbox" checked={hideRollback} onChange={(event) => setHideRollback(event.target.checked)} />
        Hide rollback
      </label>
      {request.status !== 'ready' ? <RequestFeedback request={request} loading="Loading history…" /> :
        request.data.entries.length === 0 ? <p className="feedback">No execution attempts to show.</p> : (
          <ul className="resource-list" aria-label="Execution attempts">
            {request.data.entries.map((attempt) => (
              <li className="resource-row" key={attempt.id}>
                <div className="migration-link">
                  <div>
                    <Link to={`/databases/${dbID}/migrations/${attempt.migration_id}`}>
                      {names.get(attempt.migration_id) ?? `Migration #${attempt.migration_id}`}
                    </Link>
                    {' · '}{operationLabels[attempt.operation]}{' · '}{attempt.status === 'success' ? 'Succeeded' : 'Failed'}
                  </div>
                  <time dateTime={attempt.created_at}>{new Date(attempt.created_at).toLocaleString()}</time>
                </div>
                <details>
                  <summary>Attempt details</summary>
                  <dl className="migration-summary">
                    <dt>Revision</dt><dd>{attempt.revision}</dd>
                    <dt>Actor</dt><dd>#{attempt.actor_id}</dd>
                    <dt>Execution</dt><dd>{attempt.execution_id}</dd>
                    <dt>Attempt</dt><dd>{attempt.attempt_id}</dd>
                    {attempt.recovery_of_execution_id && <><dt>Original execution</dt><dd>{attempt.recovery_of_execution_id}</dd></>}
                  </dl>
                  {attempt.error_message && <p className="feedback feedback-error">{attempt.error_message}</p>}
                  <pre className="sql-view" tabIndex={0}>{attempt.executed_sql}</pre>
                </details>
              </li>
            ))}
          </ul>
        )}
    </section>
  )
}

function MigrationDetail({ dbID, migrationID }: { dbID: string; migrationID: string }) {
  const request = useRequest(getMigration, dbID, migrationID)
  if (request.status !== 'ready') return <RequestFeedback request={request} loading="Loading migration…" />
  const migration = request.data

  return (
    <>
      <h1>{migration.title}</h1>
      <dl className="migration-summary">
        <dt>Revision</dt><dd>{migration.revision}</dd>
        {migration.status !== 'draft' && <><dt>Migration state</dt><dd>{migration.status.replaceAll('_', ' ')}</dd></>}
        {migration.version != null && <><dt>Version</dt><dd>{migration.version}</dd></>}
        <dt>Created</dt><dd><time dateTime={migration.created_at}>{new Date(migration.created_at).toLocaleString()}</time></dd>
        {migration.executed_at && (
          <><dt>Executed</dt><dd><time dateTime={migration.executed_at}>{new Date(migration.executed_at).toLocaleString()}</time></dd></>
        )}
      </dl>
      {migration.error_message && <p className="feedback feedback-error">{migration.error_message}</p>}
      <h2>SQL</h2>
      <pre className="sql-view" role="region" aria-label="Migration SQL" tabIndex={0}><code>{migration.sql}</code></pre>
      <MigrationWorkflow dbID={dbID} migrationID={migrationID} migration={migration} />
    </>
  )
}
