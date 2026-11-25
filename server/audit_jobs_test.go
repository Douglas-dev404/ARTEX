package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/auditjob"
	"github.com/Autumn-27/artex/db"
	"github.com/google/uuid"
)

func TestAuditJobAPICredentialSeparation(t *testing.T) {
	dsn := os.Getenv("ARTEX_AUDIT_TEST_DSN")
	if dsn == "" {
		t.Skip("set ARTEX_AUDIT_TEST_DSN to a disposable PostgreSQL database")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	runnerToken := strings.Repeat("r", 32)
	t.Setenv(auditjob.TokenEnv, runnerToken)
	s := &Server{m: &Manager{pg: d}, jwtKey: []byte("audit-test-jwt-key")}
	adminToken, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	call := func(method, path, token string, body any, want int, result any) {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d, want %d; body: %s", method, path, w.Code, want, w.Body)
		}
		if result != nil {
			if err := json.Unmarshal(w.Body.Bytes(), result); err != nil {
				t.Fatal(err)
			}
		}
	}

	const jobs = "/api/audit-jobs"
	const claimPath = auditjob.RunnerPrefix + "claim"
	call(http.MethodPost, claimPath, adminToken, nil, 401, nil)
	call(http.MethodPost, jobs, runnerToken, nil, 401, nil)

	request := auditjob.Submit{ID: uuid.New(), Artifact: []byte{}, ExpectedSHA256: strings.Repeat("a", 64)}
	defer d.Exec(`DELETE FROM audit_jobs WHERE id = $1`, request.ID)
	call(http.MethodPost, jobs, adminToken, request, 400, nil)
	request.Operation = auditjob.VerifySHA256
	call(http.MethodPost, jobs, adminToken, request, 202, nil)
	var claim auditjob.Claim
	call(http.MethodPost, claimPath, runnerToken, nil, 200, &claim)
	if claim.ID != request.ID || claim.Artifact == nil || len(claim.Artifact) != 0 {
		t.Fatalf("runner received an invalid empty-artifact claim: %+v", claim)
	}

	digest := sha256.Sum256(claim.Artifact)
	completion := auditjob.Completion{LeaseID: claim.LeaseID, ActualSHA256: hex.EncodeToString(digest[:])}
	call(http.MethodPost, jobs+"/"+claim.ID.String()+"/cancel", adminToken, nil, 200, nil)
	call(http.MethodPost, auditjob.RunnerPrefix+claim.ID.String()+"/complete", runnerToken, completion, 409, nil)

	request.ID = uuid.New()
	defer d.Exec(`DELETE FROM audit_jobs WHERE id = $1`, request.ID)
	request.ExpectedSHA256 = completion.ActualSHA256
	call(http.MethodPost, jobs, adminToken, request, 202, nil)
	call(http.MethodPost, claimPath, runnerToken, nil, 200, &claim)
	completion.LeaseID = claim.LeaseID
	call(http.MethodPost, auditjob.RunnerPrefix+claim.ID.String()+"/complete", runnerToken, completion, 204, nil)
	var job auditjob.Job
	call(http.MethodGet, jobs+"/"+request.ID.String(), adminToken, nil, 200, &job)
	if job.State != "succeeded" || job.Matches == nil || !*job.Matches {
		t.Fatalf("unexpected successful audit: %+v", job)
	}
}
