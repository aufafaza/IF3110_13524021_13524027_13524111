package migration

import (
	"net/http"

	"github.com/aufafaza/IF3110_13524021_13524027_13524111/internal/access"
)

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	viewGuard := access.RequireDBAccess(h.service.checker, access.LevelView)
	editGuard := access.RequireDBAccess(h.service.checker, access.LevelEdit)

	mux.Handle("GET /api/databases/{dbID}/migrations", viewGuard(http.HandlerFunc(h.handleListHistory)))
	mux.Handle("GET /api/databases/{dbID}/migrations/{migrationID}", viewGuard(http.HandlerFunc(h.handleDetail)))

	mux.Handle("GET /databases/{dbID}/migrations", viewGuard(http.HandlerFunc(h.handleListHistory)))
	mux.Handle("POST /databases/{dbID}/migrations", editGuard(http.HandlerFunc(h.handleCreateOrPreview)))
	mux.Handle("POST /api/databases/{dbID}/migrations", editGuard(http.HandlerFunc(h.handleCreateOrPreview)))
	mux.Handle("GET /databases/{dbID}/migrations/{migrationID}", viewGuard(http.HandlerFunc(h.handleDetail)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}", editGuard(http.HandlerFunc(h.handleUpdateOrPreview)))
	mux.Handle("POST /api/databases/{dbID}/migrations/{migrationID}", editGuard(http.HandlerFunc(h.handleUpdateOrPreview)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}/explain", editGuard(http.HandlerFunc(h.handleExplain)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}/execute", editGuard(http.HandlerFunc(h.handleExecute)))
	mux.Handle("GET /databases/{dbID}/migration-history", viewGuard(http.HandlerFunc(h.handleListAttemptHistory)))
	mux.Handle("GET /api/databases/{dbID}/migration-history", viewGuard(http.HandlerFunc(h.handleListAttemptHistory)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}/plan", editGuard(http.HandlerFunc(h.handlePlan)))
	mux.Handle("POST /api/databases/{dbID}/migrations/{migrationID}/plan", editGuard(http.HandlerFunc(h.handlePlan)))
	mux.Handle("POST /api/databases/{dbID}/migrations/{migrationID}/execute", editGuard(http.HandlerFunc(h.handleExecute)))
	mux.Handle("POST /databases/{dbID}/migrations/{migrationID}/review", editGuard(http.HandlerFunc(h.handleSubmitReview)))
	mux.Handle("GET /databases/{dbID}/migration-reviews", viewGuard(http.HandlerFunc(h.handleListReviews)))
	mux.Handle("POST /api/databases/{dbID}/migrations/{migrationID}/review", editGuard(http.HandlerFunc(h.handleSubmitReview)))
	mux.Handle("GET /api/databases/{dbID}/migration-reviews", viewGuard(http.HandlerFunc(h.handleListReviews)))
	mux.Handle("POST /migration-reviews/{requestID}/decision", http.HandlerFunc(h.handleReviewDecision))
	mux.Handle("POST /migration-reviews/{requestID}/enqueue", http.HandlerFunc(h.handleEnqueueReview))
	mux.Handle("POST /api/migration-reviews/{requestID}/decision", http.HandlerFunc(h.handleReviewDecision))
	mux.Handle("POST /api/migration-reviews/{requestID}/enqueue", http.HandlerFunc(h.handleEnqueueReview))
}
