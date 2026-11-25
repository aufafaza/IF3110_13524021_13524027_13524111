import { Link, useParams } from 'react-router-dom'
import { getMigration, listMigrations } from '@/api/migrations'
import { useRequest } from '@/api/useRequest'
import { RequestFeedback } from '@/components/RequestFeedback'

export function MigrationsPage() {
  const { dbID, migrationID } = useParams()
  if (!dbID) return <h1>Database not specified</h1>

  const databasePath = `/databases/${dbID}`
  return migrationID ? (
    <>
      <Link className="back-link" to={`${databasePath}/migrations`}>Back to migrations</Link>
      <MigrationDetail dbID={dbID} migrationID={migrationID} />
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
  if (request.data.migrations.length === 0) return <p className="feedback">No migrations available.</p>

  return (
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
        <dt>Status</dt><dd>{migration.status}</dd>
        {migration.version != null && <><dt>Version</dt><dd>{migration.version}</dd></>}
        <dt>Created</dt><dd><time dateTime={migration.created_at}>{new Date(migration.created_at).toLocaleString()}</time></dd>
        {migration.executed_at && (
          <><dt>Executed</dt><dd><time dateTime={migration.executed_at}>{new Date(migration.executed_at).toLocaleString()}</time></dd></>
        )}
      </dl>
      {migration.error_message && <p className="feedback feedback-error">{migration.error_message}</p>}
      <h2>SQL</h2>
      <pre className="sql-view" role="region" aria-label="Migration SQL" tabIndex={0}><code>{migration.sql}</code></pre>
    </>
  )
}
