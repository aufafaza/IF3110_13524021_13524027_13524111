import type { ReactNode } from 'react'

export function DataTable({ caption, headings, rows }: {
  caption: string
  headings: string[]
  rows: ReactNode[][]
}) {
  if (rows.length === 0) return <p className="feedback">No {caption.toLowerCase()} available.</p>

  return (
    <div className="table-scroll" tabIndex={0} role="region" aria-label={caption}>
      <table className="metadata-table">
        <caption>{caption}</caption>
        <thead>
          <tr>{headings.map((heading) => <th scope="col" key={heading}>{heading}</th>)}</tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr key={index}>
              {row.map((cell, column) => <td key={column}>{cell}</td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
