package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/auditjob"
	"github.com/google/uuid"
)

func TestRunnerProcessesAuditClaim(t *testing.T) {
	const expected = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	token := strings.Repeat("r", 32)
	claim := auditjob.Claim{ID: uuid.New(), LeaseID: uuid.New(), Artifact: []byte("abc")}
	completed := make(chan auditjob.Completion, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+auditjob.RunnerPrefix+"claim", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if err := json.NewEncoder(w).Encode(claim); err != nil {
			t.Error(err)
		}
	})
	mux.HandleFunc("POST "+auditjob.RunnerPrefix+claim.ID.String()+"/complete", func(w http.ResponseWriter, r *http.Request) {
		var result auditjob.Completion
		if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		select {
		case completed <- result:
		default:
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := newRunnerClient(server.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.run(ctx) }()
	select {
	case result := <-completed:
		cancel()
		if result.LeaseID != claim.LeaseID || result.ActualSHA256 != expected {
			t.Errorf("unexpected result: %+v", result)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	case err := <-done:
		t.Fatalf("runner exited without completing the job: %v", err)
	}
}
