package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Autumn-27/artex/auditjob"
	"github.com/Autumn-27/artex/db"
	"github.com/google/uuid"
)

func (s *Server) registerAuditJobs(api, root *http.ServeMux) {
	token := os.Getenv(auditjob.TokenEnv)
	if token == "" {
		return
	}
	if !auditjob.ValidToken(token) {
		slog.Error("audit routes disabled: invalid runner token", "variable", auditjob.TokenEnv)
		return
	}

	api.HandleFunc("POST /api/audit-jobs", s.submitAuditJob)
	api.HandleFunc("GET /api/audit-jobs/{id}", s.getAuditJob)
	api.HandleFunc("POST /api/audit-jobs/{id}/cancel", s.cancelAuditJob)

	runners := http.NewServeMux()
	runners.HandleFunc("POST "+auditjob.RunnerPrefix+"claim", s.claimAuditJob)
	runners.HandleFunc("POST "+auditjob.RunnerPrefix+"{id}/complete", s.completeAuditJob)
	root.Handle(auditjob.RunnerPrefix, authenticateAuditRunner(token, runners))
}

func authenticateAuditRunner(token string, next http.Handler) http.Handler {
	expected := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		provided, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		actual := sha256.Sum256([]byte(provided))
		if !ok || subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			writeErr(w, http.StatusUnauthorized, "credencial do runner inválida")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func decodeAuditJSON(w http.ResponseWriter, r *http.Request, size int64, dest any) bool {
	if r.ContentLength > size {
		writeErr(w, http.StatusRequestEntityTooLarge, "payload de auditoria acima do limite")
		return false
	}
	controller := http.NewResponseController(w)
	if controller.SetReadDeadline(time.Now().Add(15*time.Second)) == nil {
		defer controller.SetReadDeadline(time.Time{})
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, size))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(dest)
	if err == nil {
		err = decoder.Decode(new(json.RawMessage))
		if errors.Is(err, io.EOF) {
			return true
		}
	}
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		writeErr(w, http.StatusRequestEntityTooLarge, "payload de auditoria acima do limite")
		return false
	}
	writeErr(w, http.StatusBadRequest, "payload de auditoria inválido")
	return false
}

func parseAuditJobID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		writeErr(w, http.StatusBadRequest, "ID de auditoria inválido")
		return uuid.Nil, false
	}
	return id, true
}

func writeAuditError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusNotFound, "auditoria não encontrada")
	case errors.Is(err, db.ErrAuditJobConflict):
		writeErr(w, http.StatusConflict, "ID já utilizado para outro conteúdo")
	case errors.Is(err, db.ErrAuditQueueFull):
		w.Header().Set("Retry-After", "5")
		writeErr(w, http.StatusTooManyRequests, "fila de auditoria cheia")
	default:
		slog.Error("audit job database error", "error", err)
		writeErr(w, http.StatusInternalServerError, "erro ao processar auditoria")
	}
}

func (s *Server) submitAuditJob(w http.ResponseWriter, r *http.Request) {
	var request auditjob.Submit
	if !decodeAuditJSON(w, r, auditjob.MaxJobJSON, &request) {
		return
	}
	if len(request.Artifact) > auditjob.MaxArtifact {
		writeErr(w, http.StatusRequestEntityTooLarge, "artefato acima de 1 MiB")
		return
	}
	if request.ID == uuid.Nil || request.Operation != auditjob.VerifySHA256 ||
		!auditjob.ValidSHA256(request.ExpectedSHA256) || request.Artifact == nil {
		writeErr(w, http.StatusBadRequest, "informe id, operação verify_sha256, expected_sha256 e artefato em base64")
		return
	}
	if pg := s.pg(w); pg != nil {
		job, err := pg.SubmitAuditJob(r.Context(), request)
		if err != nil {
			writeAuditError(w, err)
			return
		}
		w.Header().Set("Location", "/api/audit-jobs/"+job.ID.String())
		writeJSON(w, http.StatusAccepted, job)
	}
}

func (s *Server) getAuditJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAuditJobID(w, r)
	if !ok {
		return
	}
	if pg := s.pg(w); pg != nil {
		job, err := pg.GetAuditJob(r.Context(), id)
		if err != nil {
			writeAuditError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, job)
	}
}

func (s *Server) cancelAuditJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAuditJobID(w, r)
	if !ok {
		return
	}
	if pg := s.pg(w); pg != nil {
		job, err := pg.CancelAuditJob(r.Context(), id)
		if err != nil {
			writeAuditError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, job)
	}
}

func (s *Server) claimAuditJob(w http.ResponseWriter, r *http.Request) {
	if pg := s.pg(w); pg != nil {
		claim, err := pg.ClaimAuditJob(r.Context())
		if errors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if err != nil {
			writeAuditError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, claim)
	}
}

func (s *Server) completeAuditJob(w http.ResponseWriter, r *http.Request) {
	id, ok := parseAuditJobID(w, r)
	if !ok {
		return
	}
	var completion auditjob.Completion
	if !decodeAuditJSON(w, r, 1024, &completion) {
		return
	}
	if completion.LeaseID == uuid.Nil || !auditjob.ValidSHA256(completion.ActualSHA256) {
		writeErr(w, http.StatusBadRequest, "informe concessão e SHA-256 válidos")
		return
	}
	if pg := s.pg(w); pg != nil {
		completed, err := pg.CompleteAuditJob(r.Context(), id, completion)
		if err != nil {
			writeAuditError(w, err)
			return
		}
		if !completed {
			writeErr(w, http.StatusConflict, "concessão expirada ou auditoria encerrada")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
