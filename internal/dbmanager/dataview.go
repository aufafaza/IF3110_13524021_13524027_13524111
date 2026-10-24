package dbmanager

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const (
	PageSize   = 50
	maxCellLen = 200
)

type Cell struct {
	Text string
	Null bool
}

type DataPage struct {
	Columns []string
	Rows    [][]Cell
	Page    int
	HasNext bool
}

func (v *Viewer) Rows(ctx context.Context, dbID int64, schema, table string, page int) (dp DataPage, err error) {
	if page < 1 {
		page = 1
	}
	dp.Page = page
	err = v.readTx(ctx, dbID, func(tx pgx.Tx) error {
		if err := requireTable(ctx, tx, schema, table); err != nil {
			return err
		}
		sql := "SELECT * FROM " + pgx.Identifier{schema, table}.Sanitize() + " LIMIT $1 OFFSET $2"
		rows, err := tx.Query(ctx, sql, pgx.QueryResultFormats{pgx.TextFormatCode},
			PageSize+1, (page-1)*PageSize)
		if err != nil {
			return err
		}
		defer rows.Close()
		for _, f := range rows.FieldDescriptions() {
			dp.Columns = append(dp.Columns, f.Name)
		}
		for rows.Next() {
			if len(dp.Rows) == PageSize {
				dp.HasNext = true
				break
			}
			raw := rows.RawValues()
			row := make([]Cell, len(raw))
			for i, b := range raw {
				row[i] = Cell{Text: clip(string(b)), Null: b == nil}
			}
			dp.Rows = append(dp.Rows, row)
		}
		return rows.Err()
	})
	return dp, err
}

func clip(s string) string {
	if len(s) <= maxCellLen {
		return s
	}
	if r := []rune(s); len(r) > maxCellLen {
		return string(r[:maxCellLen]) + "…"
	}
	return s
}