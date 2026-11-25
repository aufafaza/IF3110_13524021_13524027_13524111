import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ApiError } from '@/api/client'
import { listTables, type DatabaseTables } from '@/api/databases'

export function DatabaseTablesPage() {
  const { dbID } = useParams()

  return (
    <>
      <Link className="back-link" to="/databases">Back to databases</Link>
      {dbID ? <DatabaseTablesList key={dbID} dbID={dbID} /> : <h1>Database not found</h1>}
    </>
  )
}

function DatabaseTablesList({ dbID }: { dbID: string }) {
  const [result, setResult] = useState<DatabaseTables | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const controller = new AbortController()
    async function loadTables() {
      try {
        const tables = await listTables(dbID, controller.signal)
        if (!controller.signal.aborted) setResult(tables)
      } catch (failure) {
        if (controller.signal.aborted) return
        if (failure instanceof ApiError && failure.status === 403) {
          setError('You don’t have access to this database.')
          return
        }
        setError(failure instanceof ApiError ? failure.message : 'Could not load tables.')
      }
    }

    void loadTables()
    return () => controller.abort()
  }, [dbID])

  if (error) return <p className="feedback feedback-error" role="alert">{error}</p>
  if (!result) return <p className="feedback" role="status">Loading tables…</p>

  return (
    <>
      <h1>Tables for {result.database.name}</h1>
      <nav className="table-nav" aria-label="Database views">
        <Link to={`/databases/${dbID}/migrations`}>Migrations</Link>
      </nav>
      {result.tables.length === 0 ? <p className="feedback">No tables available.</p> : (
        <ul className="resource-list" role="list">
          {result.tables.map((table) => {
            const query = new URLSearchParams({
              schema: table.schema,
              table: table.name,
            })
            return (
              <li key={JSON.stringify([table.schema, table.name])}>
                <Link className="resource-link" to={`/databases/${dbID}/table?${query}`}>
                  {table.schema}.{table.name}
                </Link>
              </li>
            )
          })}
        </ul>
      )}
    </>
  )
}
