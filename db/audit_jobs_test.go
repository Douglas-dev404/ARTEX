package db

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Autumn-27/artex/auditjob"
	"github.com/google/uuid"
)

func TestAuditJobConcurrentClaimAndRecovery(t *testing.T) {
	// This test makes unscoped claims, so run it only against a dedicated database.
	dsn := os.Getenv("ARTEX_AUDIT_TEST_DSN")
	if dsn == "" {
		t.Skip("set ARTEX_AUDIT_TEST_DSN to a disposable PostgreSQL database")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	request := auditjob.Submit{
		ID: uuid.New(), Operation: auditjob.VerifySHA256,
		ExpectedSHA256: strings.Repeat("a", 64), Artifact: []byte("test artifact"),
	}
	defer d.Exec(`DELETE FROM audit_jobs WHERE id = $1`, request.ID)
	first, err := d.SubmitAuditJob(ctx, request)
	if err != nil || first.State != "queued" {
		t.Fatalf("submit returned %+v, %v", first, err)
	}
	replayed, err := d.SubmitAuditJob(ctx, request)
	if err != nil || !replayed.CreatedAt.Equal(first.CreatedAt) {
		t.Fatalf("submit replay returned %+v, %v", replayed, err)
	}
	modified := request
	modified.Artifact = []byte("changed")
	if _, err := d.SubmitAuditJob(ctx, modified); !errors.Is(err, ErrAuditJobConflict) {
		t.Fatalf("conflicting submission returned %v", err)
	}

	type claimResult struct {
		claim auditjob.Claim
		err   error
	}
	results := make(chan claimResult, 4)
	for range cap(results) {
		go func() {
			claim, err := d.ClaimAuditJob(ctx)
			results <- claimResult{claim, err}
		}()
	}
	var original auditjob.Claim
	for range cap(results) {
		result := <-results
		if errors.Is(result.err, sql.ErrNoRows) {
			continue
		}
		if result.err != nil || original.ID != uuid.Nil || result.claim.ID != request.ID {
			t.Fatalf("unexpected claim %+v, %v", result.claim, result.err)
		}
		original = result.claim
	}
	if original.ID == uuid.Nil || string(original.Artifact) != string(request.Artifact) {
		t.Fatalf("missing claim: %+v", original)
	}
	if _, err := d.ExecContext(ctx, `UPDATE audit_jobs SET available_at = now() WHERE id = $1`, request.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := d.ClaimAuditJob(ctx)
	if err != nil || reclaimed.ID != request.ID || reclaimed.LeaseID == original.LeaseID {
		t.Fatalf("reclaim returned %+v, %v", reclaimed, err)
	}
	completion := auditjob.Completion{LeaseID: original.LeaseID, ActualSHA256: request.ExpectedSHA256}
	if ok, err := d.CompleteAuditJob(ctx, request.ID, completion); ok || err != nil {
		t.Fatalf("stale lease completed: %t, %v", ok, err)
	}
	completion.LeaseID = reclaimed.LeaseID
	for range 2 {
		if ok, err := d.CompleteAuditJob(ctx, request.ID, completion); !ok || err != nil {
			t.Fatalf("valid completion returned %t, %v", ok, err)
		}
	}
	job, err := d.CancelAuditJob(ctx, request.ID)
	if err != nil || job.State != "succeeded" || job.Attempts != 2 || job.Matches == nil || !*job.Matches {
		t.Fatalf("terminal job returned %+v, %v", job, err)
	}
	var artifact []byte
	if err := d.QueryRowContext(ctx, `SELECT artifact FROM audit_jobs WHERE id = $1`, request.ID).Scan(&artifact); err != nil || artifact != nil {
		t.Fatalf("artifact retained: %t, %v", artifact != nil, err)
	}
}
