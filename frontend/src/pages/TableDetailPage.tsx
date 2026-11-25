import { Link, useParams, useSearchParams } from 'react-router-dom'
import { getTableInfo, getTableRows } from '@/api/databases'
import { DataTable } from '@/components/DataTable'
import { useRequest } from '@/api/useRequest'
import { RequestFeedback } from '@/components/RequestFeedback'

const views = ['Columns', 'Constraints', 'Indexes', 'Data'] as const
type View = typeof views[number]
type TableAddress = { dbID: string; schema: string; table: string }

export function TableDetailPage() {
  const { dbID } = useParams()
  const [query] = useSearchParams()
  const schema = query.get('schema')
  const table = query.get('table')
  const view = views.find((option) => option.toLowerCase() === query.get('tab')) ?? 'Columns'
  const requestedPage = Number(query.get('page') ?? 1)
  const page = Number.isSafeInteger(requestedPage) && requestedPage > 0 ? requestedPage : 1

  return (
    <>
      <Link className="back-link" to={dbID ? `/databases/${dbID}` : '/databases'}>
        {dbID ? 'Back to tables' : 'Back to databases'}
      </Link>
      {dbID && schema && table ? (
        <>
          <h1>{schema}.{table}</h1>
          <nav className="table-nav" aria-label="Table views">
            {views.map((option) => (
              <Link key={option} aria-current={view === option ? 'page' : undefined}
                to={tableQuery(schema, table, option)}>
                {option}
              </Link>
            ))}
          </nav>
          {view === 'Data' ? (
            <TableData dbID={dbID} schema={schema} table={table} page={page} />
          ) : (
            <TableMetadata dbID={dbID} schema={schema} table={table} view={view} />
          )}
        </>
      ) : <h1>Table not specified</h1>}
    </>
  )
}

function tableQuery(schema: string, table: string, view: View, page = 1) {
  const query = new URLSearchParams({ schema, table, tab: view.toLowerCase() })
  if (page > 1) query.set('page', String(page))
  return `?${query}`
}

function TableMetadata({ dbID, schema, table, view }: TableAddress & { view: View }) {
  const request = useRequest(getTableInfo, dbID, schema, table)
  if (request.status !== 'ready') return <RequestFeedback request={request} loading="Loading table metadata…" />
  const info = request.data

  switch (view) {
    case 'Constraints':
      return <DataTable caption={view} headings={['Name', 'Type', 'Definition']}
        rows={info.constraints.map(({ name, type, definition }) => [name, type, definition])} />
    case 'Indexes':
      return <DataTable caption={view} headings={['Name', 'Method', 'Unique', 'Definition']}
        rows={info.indexes.map(({ name, method, unique, definition }) => [name, method, unique ? 'Yes' : 'No', definition])} />
    default:
      return <DataTable caption="Columns" headings={['Name', 'Type', 'Nullable', 'Default']}
        rows={info.columns.map((column) => [column.name, column.type, column.nullable ? 'Yes' : 'No', column.default ?? '—'])} />
  }
}

function TableData({ dbID, schema, table, page }: TableAddress & { page: number }) {
  const request = useRequest(getTableRows, dbID, schema, table, page)
  if (request.status !== 'ready') return <RequestFeedback request={request} loading="Loading rows…" />
  const data = request.data

  return (
    <>
      <DataTable caption="Rows" headings={data.columns}
        rows={data.rows.map((row) => row.map((cell) => cell.null ? 'NULL' : cell.text))} />
      <nav className="table-nav pagination" aria-label="Row pages">
        {data.page > 1 && <Link to={tableQuery(schema, table, 'Data', data.page - 1)}>Previous</Link>}
        <span>Page {data.page}</span>
        {data.has_next && <Link to={tableQuery(schema, table, 'Data', data.page + 1)}>Next</Link>}
      </nav>
    </>
  )
}
