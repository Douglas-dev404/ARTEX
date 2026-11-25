package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"

	"github.com/Autumn-27/artex/auditjob"
	"github.com/google/uuid"
)

const (
	auditAdmissionLock int64 = 7337741005
	auditQueueLimit          = 128
)

var (
	ErrAuditQueueFull   = errors.New("audit queue is full")
	ErrAuditJobConflict = errors.New("audit job ID already used for different input")
)

const auditJobColumns = `id, state, expected_sha256, actual_sha256, attempts, created_at, finished_at`

func scanAuditJob(row interface{ Scan(...any) error }) (auditjob.Job, error) {
	var job auditjob.Job
	err := row.Scan(&job.ID, &job.State, &job.ExpectedSHA256, &job.ActualSHA256,
		&job.Attempts, &job.CreatedAt, &job.FinishedAt)
	if job.ActualSHA256 != nil {
		matches := *job.ActualSHA256 == job.ExpectedSHA256
		job.Matches = &matches
	}
	return job, err
}

// Expire a bounded batch so an idle runner or a new submission can release
// payloads belonging to jobs that exhausted their deadline or retry budget.
const expireAuditJobsSQL = `
WITH expired AS (
    SELECT id FROM audit_jobs
    WHERE state IN ('queued', 'running')
      AND (deadline_at <= now() OR (attempts >= 3 AND available_at <= now()))
    LIMIT 128 FOR UPDATE SKIP LOCKED
)
UPDATE audit_jobs AS job
SET state = 'failed', artifact = NULL, lease_id = NULL,
    finished_at = CASE WHEN attempts >= 3 THEN least(deadline_at, available_at) ELSE deadline_at END
FROM expired WHERE job.id = expired.id`

func (d *DB) SubmitAuditJob(ctx context.Context, request auditjob.Submit) (auditjob.Job, error) {
	// The expected digest has a fixed width; include it in the replay fingerprint.
	fingerprint := sha256.New()
	fingerprint.Write([]byte(request.ExpectedSHA256))
	fingerprint.Write(request.Artifact)
	requestHash := hex.EncodeToString(fingerprint.Sum(nil))

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return auditjob.Job{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, auditAdmissionLock); err != nil {
		return auditjob.Job{}, err
	}
	if _, err := tx.ExecContext(ctx, expireAuditJobsSQL); err != nil {
		return auditjob.Job{}, err
	}
	var existingHash string
	err = tx.QueryRowContext(ctx, `SELECT request_hash FROM audit_jobs WHERE id = $1`, request.ID).Scan(&existingHash)
	if err == nil {
		if existingHash != requestHash {
			return auditjob.Job{}, ErrAuditJobConflict
		}
		job, err := scanAuditJob(tx.QueryRowContext(ctx, `SELECT `+auditJobColumns+` FROM audit_jobs WHERE id = $1`, request.ID))
		if err != nil {
			return auditjob.Job{}, err
		}
		return job, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return auditjob.Job{}, err
	}
	job, err := scanAuditJob(tx.QueryRowContext(ctx, `
INSERT INTO audit_jobs (id, request_hash, expected_sha256, artifact)
SELECT $1, $2, $3, $4
WHERE (SELECT count(*) FROM audit_jobs WHERE state IN ('queued', 'running')) < $5
RETURNING `+auditJobColumns, request.ID, requestHash, request.ExpectedSHA256, request.Artifact, auditQueueLimit))
	if errors.Is(err, sql.ErrNoRows) {
		return auditjob.Job{}, ErrAuditQueueFull
	}
	if err != nil {
		return auditjob.Job{}, err
	}
	return job, tx.Commit()
}

func (d *DB) GetAuditJob(ctx context.Context, id uuid.UUID) (auditjob.Job, error) {
	return scanAuditJob(d.QueryRowContext(ctx, `
SELECT id,
    CASE WHEN state IN ('queued', 'running') AND due.at <= now() THEN 'failed' ELSE state END,
    expected_sha256, actual_sha256, attempts, created_at,
    CASE WHEN state IN ('queued', 'running') AND due.at <= now() THEN due.at ELSE finished_at END
FROM audit_jobs
CROSS JOIN LATERAL (SELECT CASE WHEN attempts >= 3 THEN least(deadline_at, available_at) ELSE deadline_at END AS at) due
WHERE id = $1`, id))
}

func (d *DB) CancelAuditJob(ctx context.Context, id uuid.UUID) (auditjob.Job, error) {
	job, err := scanAuditJob(d.QueryRowContext(ctx, `
UPDATE audit_jobs SET state = CASE WHEN deadline_at <= now() OR (attempts >= 3 AND available_at <= now())
    THEN 'failed' ELSE 'cancelled' END, artifact = NULL, lease_id = NULL,
    finished_at = least(now(), deadline_at, CASE WHEN attempts >= 3 THEN available_at END)
WHERE id = $1 AND state IN ('queued', 'running') RETURNING `+auditJobColumns, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d.GetAuditJob(ctx, id)
	}
	return job, err
}

func (d *DB) ClaimAuditJob(ctx context.Context) (auditjob.Claim, error) {
	if _, err := d.ExecContext(ctx, expireAuditJobsSQL); err != nil {
		return auditjob.Claim{}, err
	}
	var claim auditjob.Claim
	err := d.QueryRowContext(ctx, `
WITH candidate AS (
    SELECT id FROM audit_jobs WHERE state IN ('queued', 'running')
      AND available_at <= now() AND deadline_at > now() AND attempts < 3
    ORDER BY available_at, created_at, id LIMIT 1 FOR UPDATE SKIP LOCKED
)
UPDATE audit_jobs AS job SET state = 'running', attempts = attempts + 1,
    lease_id = $1, available_at = now() + interval '30 seconds'
FROM candidate WHERE job.id = candidate.id
RETURNING job.id, job.lease_id, job.artifact`, uuid.New()).Scan(&claim.ID, &claim.LeaseID, &claim.Artifact)
	return claim, err
}

func (d *DB) CompleteAuditJob(ctx context.Context, id uuid.UUID, completion auditjob.Completion) (bool, error) {
	var completed bool
	err := d.QueryRowContext(ctx, `
UPDATE audit_jobs SET state = 'succeeded', actual_sha256 = $3, artifact = NULL, finished_at = coalesce(finished_at, now())
WHERE id = $1 AND lease_id = $2 AND (
    (state = 'running' AND available_at > now() AND deadline_at > now())
    OR (state = 'succeeded' AND actual_sha256 = $3)
)
RETURNING true`, id, completion.LeaseID, completion.ActualSHA256).Scan(&completed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return completed, err
}
