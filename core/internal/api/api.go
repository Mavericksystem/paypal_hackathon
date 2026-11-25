package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/Mavericksystem/warden/core/internal/intents"
	"github.com/Mavericksystem/warden/core/internal/ledger"
	"github.com/Mavericksystem/warden/core/internal/policystore"
)

const maxBodyBytes = 64 << 10

type IntenService interface {
	Create(ctx context.Context, in intents.CreateInput) (intents.Intent, error)
	Get(ctx context.Context, id string) (intents.Intent, error)
}

type Server struct {
	svc intents.Service
	log *slog.Logger
}

func New(svc IntentService, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	s := &Server{svc: svc, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /intents", s.createIntent)
	mux.HandleFunc("GET /intents/{id}", s.getIntent)
	mux.HandleFunc("GET /healthz", s.healthz)
	return s.accesLog(s.recover(mux))
}

type createRequest struct {
	AgentID        string `json:"agent_id"`
	BudgetID       string `json:"budget_id"`
	IdempotencyKey string `json:"idempotency_key"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	Merchant       string `json:"merchant"`
	Recipient      string `json:"recipient"`
	Goal           string `json:"goal"`
}

type intentResponse struct {
	ID             string `json:"id"`
	AgentID        string `json:"agent_id"`
	BudgetID       string `json:"budget_id"`
	AmountMinor    int64  `json:"amount_minor"`
	State          string `json:"state"`
	Currency       string `json:"currency"`
	Merchant       string `json:"merchant"`
	Recipient      string `json:"recipient"`
	Goal           string `json:"goal"`
	IdempotencyKey string `json:"idempotency_key"`
	Version        int    `json:"version"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

func toResponse(in intents.Intent) intentResponse {
	return intentResponse{
		ID:             in.ID,
		AgentID:        in.AgentID,
		BudgetID:       in.BudgetID,
		AmountMinor:    in.AmountMinor,
		State:          string(in.State),
		Currency:       in.Currency,
		Merchant:       in.Merchant,
		Recipient:      in.Recipient,
		Goal:           in.Goal,
		IdempotencyKey: in.IdempotencyKey,
		Version:        in.Version,
		CreatedAt:      in.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:      in.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) createIntent(w http.ReponseWriter, r *http.Request) {
	var req createRequest
	if err := decodeJSON(w, r, &req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	got, err := s.svc.Create(r.Context(), intents.CreateInput{
		AgentID:        req.AgentID,
		BudgetID:       req.BudgetID,
		IdempotencyKey: req.IdempotencyKey,
		AmountMinor:    req.AmountMinor,
		Currency:       req.Currency,
		Merchant:       req.Merchant,
		Recipient:      req.Recipient,
		Goal:           req.Goal,
	})
	if err != nil {
		s.writeServiceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toResponse(got))
}

func (s *Server) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, intents.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
	case errors.Is(err, ledger.ErrBudgetNotFound):
		writeError(w, http.StatusNotFound, "budget_not_found", "budget not found")
	case errors.Is(err, intents.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "intent not found")
	case errors.Is(err, intents.ErrIdempotencyConflict):
		writeError(w, http.StatusConflict, "idempotency_conflict", err.Error())
	case errors.Is(err, intents.ErrStateConflict):
		writeError(w, http.StatusConflict, "state_conflict", "the intent changed concurrently, retry the same request")
	case errors.Is(err, policystore.ErrNoPolicy):
		writeError(w, http.StatusForbidden, "no_policy",
			"this agent has no policy on this budget, nothing was decided, retry the same request once a policy exists")
	default:
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is empty")
		}
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return err
		}
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
