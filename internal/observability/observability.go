package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
	"github.com/jackc/pgx/v5"
)

type contextKey struct{}

type identifiers struct {
	correlationID string
	traceID       string
	spanID        string
}

type Span struct {
	ctx       context.Context
	logger    *slog.Logger
	name      string
	startedAt time.Time
	ids       identifiers
}

func newID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strings.Repeat("0", n*2)
	}
	return hex.EncodeToString(b)
}

func WithIdentifiers(ctx context.Context, correlationID, traceID, spanID string) context.Context {
	if correlationID == "" {
		correlationID = newID(16)
	}
	if traceID == "" {
		traceID = newID(16)
	}
	if spanID == "" {
		spanID = newID(8)
	}
	return context.WithValue(ctx, contextKey{}, identifiers{correlationID, traceID, spanID})
}

func identifiersFromContext(ctx context.Context) identifiers {
	ids, _ := ctx.Value(contextKey{}).(identifiers)
	if ids.correlationID == "" {
		ids.correlationID = newID(16)
	}
	if ids.traceID == "" {
		ids.traceID = newID(16)
	}
	if ids.spanID == "" {
		ids.spanID = newID(8)
	}
	return ids
}

func CorrelationID(ctx context.Context) string { return identifiersFromContext(ctx).correlationID }
func TraceID(ctx context.Context) string       { return identifiersFromContext(ctx).traceID }
func SpanID(ctx context.Context) string        { return identifiersFromContext(ctx).spanID }

func Start(ctx context.Context, name string) (context.Context, *Span) {
	ids := identifiersFromContext(ctx)
	spanID := newID(8)
	ctx = WithIdentifiers(ctx, ids.correlationID, ids.traceID, spanID)
	return ctx, &Span{ctx: ctx, logger: slog.Default(), name: name, startedAt: time.Now(), ids: identifiers{ids.correlationID, ids.traceID, spanID}}
}

func (s *Span) End(err error) {
	if s == nil {
		return
	}
	attrs := []any{
		"event", "span.end",
		"span_name", s.name,
		"duration_ms", float64(time.Since(s.startedAt).Microseconds()) / 1000,
		"correlation_id", s.ids.correlationID,
		"trace_id", s.ids.traceID,
		"span_id", s.ids.spanID,
	}
	if err != nil {
		attrs = append(attrs, "status", "error", "error", err.Error())
	} else {
		attrs = append(attrs, "status", "ok")
	}
	s.logger.InfoContext(s.ctx, "span", attrs...)
}

func Log(ctx context.Context, level slog.Level, event string, attrs ...any) {
	ids := identifiersFromContext(ctx)
	attrs = append(attrs,
		"event", event,
		"correlation_id", ids.correlationID,
		"trace_id", ids.traceID,
		"span_id", ids.spanID,
	)
	slog.Default().Log(ctx, level, event, attrs...)
}

// ConfigureJSON installs one JSON logger for both slog and the legacy log
// calls that still exist in the M1 handlers. This keeps application output
// machine-readable without moving credentials or session data to PostgreSQL.
func ConfigureJSON(w io.Writer) {
	logger := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	log.SetOutput(stdLogWriter{logger: logger})
	log.SetFlags(0)
}

type stdLogWriter struct{ logger *slog.Logger }

func (w stdLogWriter) Write(p []byte) (int, error) {
	w.logger.Info("legacy_log", "message", strings.TrimSpace(string(p)))
	return len(p), nil
}

type queryStart struct {
	started time.Time
	sql     string
	args    []any
}
type queryStartKey struct{}
type batchStartKey struct{}

// NewPGXTracer instruments Query, QueryRow, Exec and batch statements for
// both the platform pool and managed-database pools.
func NewPGXTracer() pgx.QueryTracer { return pgxTracer{} }

type pgxTracer struct{}

func (pgxTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, queryStartKey{}, queryStart{started: time.Now(), sql: data.SQL, args: data.Args})
}

func (pgxTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	started, _ := ctx.Value(queryStartKey{}).(queryStart)
	if started.started.IsZero() {
		started.started = time.Now()
	}
	rows := int64(0)
	if data.Err == nil {
		rows = data.CommandTag.RowsAffected()
	}
	SQL(ctx, started.sql, redactArgs(started.args), started.started, rows, data.Err, sqlState(data.Err), "phase", "query")
}

func (pgxTracer) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	return context.WithValue(ctx, batchStartKey{}, time.Now())
}

func (pgxTracer) TraceBatchQuery(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchQueryData) {
	started, _ := ctx.Value(batchStartKey{}).(time.Time)
	if started.IsZero() {
		started = time.Now()
	}
	rows := int64(0)
	if data.Err == nil {
		rows = data.CommandTag.RowsAffected()
	}
	SQL(ctx, data.SQL, redactArgs(data.Args), started, rows, data.Err, sqlState(data.Err), "phase", "batch")
}

func (pgxTracer) TraceBatchEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceBatchEndData) {
	if data.Err != nil {
		Log(ctx, slog.LevelError, "sql.batch", "status", "error", "error", data.Err.Error())
		return
	}
	Log(ctx, slog.LevelInfo, "sql.batch", "status", "success")
}

func redactArgs(args []any) map[string]any {
	out := make(map[string]any, len(args))
	for i, arg := range args {
		key := fmt.Sprintf("arg_%d", i+1)
		// Positional SQL arguments do not carry field names. Strings and byte
		// slices are therefore redacted by default; numeric values remain useful
		// for diagnostics without exposing credentials or user data.
		switch arg.(type) {
		case string, []byte, []int64:
			out[key] = "[REDACTED]"
		default:
			out[key] = arg
		}
	}
	return out
}

func sqlState(err error) string {
	if err == nil {
		return ""
	}
	if state, ok := err.(interface{ SQLState() string }); ok {
		return state.SQLState()
	}
	return ""
}

var sensitiveName = regexp.MustCompile(`(?i)(password|passwd|secret|token|session|credential|authorization|cookie|api[_-]?key)`)

func Redact(key string, value any) any {
	if sensitiveName.MatchString(key) {
		return "[REDACTED]"
	}
	return value
}

func RedactParams(params map[string]any) map[string]any {
	out := make(map[string]any, len(params))
	for key, value := range params {
		out[key] = Redact(key, value)
	}
	return out
}

func SQL(ctx context.Context, query string, params map[string]any, started time.Time, rows int64, err error, sqlState string, attrs ...any) {
	fields := []any{
		"query", query,
		"parameters", RedactParams(params),
		"duration_ms", float64(time.Since(started).Microseconds()) / 1000,
		"row_count", rows,
	}
	if err != nil {
		fields = append(fields, "status", "error", "error", err.Error())
		if sqlState != "" {
			fields = append(fields, "sqlstate", sqlState)
		}
	} else {
		fields = append(fields, "status", "success")
	}
	fields = append(fields, attrs...)
	Log(ctx, slog.LevelInfo, "sql", fields...)
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := r.Header.Get("X-Correlation-ID")
		traceID, spanID := parseTraceparent(r.Header.Get("traceparent"))
		ctx := WithIdentifiers(r.Context(), correlationID, traceID, spanID)
		ctx, span := Start(ctx, "http "+r.Method+" "+r.URL.Path)
		w.Header().Set("X-Correlation-ID", CorrelationID(ctx))
		attrs := []any{"method", r.Method, "path", r.URL.Path, "status", "started"}
		if user, ok := access.UserFromContext(ctx); ok {
			attrs = append(attrs, "actor", user.ID)
		}
		Log(ctx, slog.LevelInfo, "http.request", attrs...)
		next.ServeHTTP(w, r.WithContext(ctx))
		Log(ctx, slog.LevelInfo, "http.result", "method", r.Method, "path", r.URL.Path, "status", "completed")
		span.End(nil)
	})
}

func parseTraceparent(value string) (string, string) {
	parts := strings.Split(value, "-")
	if len(parts) == 4 && len(parts[1]) == 32 && len(parts[2]) == 16 {
		return parts[1], parts[2]
	}
	return newID(16), newID(8)
}

func Traceparent(ctx context.Context) string {
	return fmt.Sprintf("00-%s-%s-01", TraceID(ctx), SpanID(ctx))
}
