package observability

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

var sqlKeywords = strings.Fields("SELECT INSERT INTO VALUES UPDATE DELETE FROM WHERE JOIN LEFT RIGHT INNER OUTER ON AND OR NOT NULL AS SET IN IS RETURNING CREATE ALTER DROP TABLE INDEX VIEW SCHEMA FUNCTION EXISTS IF PRIMARY KEY REFERENCES UNIQUE CHECK WITH MATERIALIZED CASE WHEN THEN ELSE END ORDER BY LIMIT OFFSET ASC DESC NOWAIT LOCK FOR SHARE BEGIN START TRANSACTION COMMIT ROLLBACK SAVEPOINT RELEASE EXPLAIN ANALYZE EVENTS UNION ALL DISTINCT HAVING GROUP COUNT SUM MIN MAX COALESCE DEFAULT ANY INCREMENT GRANT REVOKE TRUNCATE DO PREPARE VACUUM")

func wordPart(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 128
}

func sqlShape(sql string) string {
	var parts []string
	for i := 0; i < len(sql) && len(parts) < 128; {
		tail := sql[i:]
		switch {
		case strings.HasPrefix(tail, "--"):
			end := strings.IndexAny(tail, "\r\n")
			if end < 0 {
				return strings.Join(parts, " ")
			}
			i += end
		case strings.HasPrefix(tail, "/*"):
			i += 2
			for depth := 1; i < len(sql) && depth > 0; {
				switch {
				case strings.HasPrefix(sql[i:], "/*"):
					depth++
					i += 2
				case strings.HasPrefix(sql[i:], "*/"):
					depth--
					i += 2
				default:
					i++
				}
			}
		case tail[0] == '\'' || tail[0] == '"':
			quote := tail[0]
			i++
			for i < len(sql) {
				c := sql[i]
				i++
				if c == '\\' && quote == '\'' && i < len(sql) {
					i++
				} else if c == quote {
					if i < len(sql) && sql[i] == quote {
						i++
					} else {
						break
					}
				}
			}
			parts = append(parts, "?")
		case tail[0] == '$':
			i++
			j := i
			for j < len(sql) && wordPart(sql[j]) {
				j++
			}
			if (j == i || sql[i] < '0' || sql[i] > '9') && j < len(sql) && sql[j] == '$' {
				tag := sql[i-1 : j+1]
				end := strings.Index(sql[j+1:], tag)
				if end < 0 {
					i = len(sql)
				} else {
					i = j + 1 + end + len(tag)
				}
			} else {
				i = j
			}
			parts = append(parts, "?")
		case wordPart(tail[0]):
			j := i + 1
			for j < len(sql) && wordPart(sql[j]) {
				j++
			}
			word := strings.ToUpper(sql[i:j])
			if isSQLKeyword(word) {
				parts = append(parts, word)
			} else {
				parts = append(parts, "?")
			}
			i = j
		default:
			if strings.ContainsRune("()[],=<>+-*/.:%;", rune(tail[0])) {
				parts = append(parts, string(tail[0]))
			}
			i++
		}
	}
	return strings.Join(parts, " ")
}

func isSQLKeyword(word string) bool {
	for _, keyword := range sqlKeywords {
		if word == keyword {
			return true
		}
	}
	return false
}

type Query struct {
	ctx        context.Context
	span       *Span
	started    time.Time
	query      string
	event      string
	parameters map[string]string
}

func StartQuery(ctx context.Context, sql string, parameterCount int) (context.Context, *Query) {
	shape := sqlShape(sql)
	event := "sql"
	word, _, _ := strings.Cut(shape, " ")
	switch word {
	case "BEGIN", "START", "COMMIT", "ROLLBACK", "END":
		event = "transaction." + strings.ToLower(word)
	}
	ctx, span := Start(ctx, "db."+event, trace.WithAttributes(attribute.String("db.system.name", "postgresql")),
		trace.WithAttributes(attribute.String("db.operation.name", word)))
	parameters := make(map[string]string, parameterCount)
	for i := range parameterCount {
		parameters[fmt.Sprintf("arg_%d", i+1)] = "[REDACTED]"
	}
	q := &Query{ctx: ctx, span: span, started: time.Now(), event: event, query: shape, parameters: parameters}
	Log(ctx, slog.LevelInfo, event+".start", "query", shape, "status", "started")
	return ctx, q
}

func (q *Query) End(rows int64, err error) {
	status := "success"
	fields := []any{
		"query", q.query, "parameters", q.parameters, "row_count", rows,
		"duration_ms", float64(time.Since(q.started).Microseconds()) / 1000,
	}
	q.span.SetAttributes(attribute.Int64("db.response.returned_rows", rows))
	if err != nil {
		status = "error"
		fields = append(fields, "error", safeError(err))
		if code := SQLState(err); code != "" {
			fields = append(fields, "sqlstate", code)
			q.span.SetAttributes(attribute.String("db.response.status_code", code))
		}
	}
	Log(q.ctx, slog.LevelInfo, q.event+".result", append(fields, "status", status)...)
	q.span.End(err)
}

type queryKey struct{}
type batchKey struct{}
type pgxTracer struct{}

func NewPGXTracer() pgx.QueryTracer { return pgxTracer{} }

func (pgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx, q := StartQuery(ctx, data.SQL, len(data.Args))
	return context.WithValue(ctx, queryKey{}, q)
}

func (pgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if q, ok := ctx.Value(queryKey{}).(*Query); ok {
		q.End(data.CommandTag.RowsAffected(), data.Err)
	}
}

func (pgxTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	ctx, span := Start(ctx, "db.pgx_batch")
	return context.WithValue(ctx, batchKey{}, span)
}

func (pgxTracer) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	_, q := StartQuery(ctx, data.SQL, len(data.Args))
	q.End(data.CommandTag.RowsAffected(), data.Err)
}

func (pgxTracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchEndData) {
	if span, ok := ctx.Value(batchKey{}).(*Span); ok {
		span.End(data.Err)
	}
}
