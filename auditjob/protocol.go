// Package auditjob defines the wire format for offline artifact audits.
package auditjob

import (
	"encoding/hex"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	VerifySHA256 = "verify_sha256"
	MaxArtifact  = 1 << 20
	MaxJobJSON   = 1_450_000 // A base64-encoded MaxArtifact plus job metadata.
	RunnerPrefix = "/api/internal/audit-runner/"
	TokenEnv     = "ARTEX_AUDIT_RUNNER_TOKEN"
)

type Submit struct {
	ID             uuid.UUID `json:"id"`
	Operation      string    `json:"operation"`
	ExpectedSHA256 string    `json:"expected_sha256"`
	Artifact       []byte    `json:"artifact"`
}

type Job struct {
	ID             uuid.UUID  `json:"id"`
	State          string     `json:"state"`
	ExpectedSHA256 string     `json:"expected_sha256"`
	ActualSHA256   *string    `json:"actual_sha256,omitempty"`
	Matches        *bool      `json:"matches,omitempty"`
	Attempts       int        `json:"attempts"`
	CreatedAt      time.Time  `json:"created_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type Claim struct {
	ID       uuid.UUID `json:"id"`
	LeaseID  uuid.UUID `json:"lease_id"`
	Artifact []byte    `json:"artifact"`
}

type Completion struct {
	LeaseID      uuid.UUID `json:"lease_id"`
	ActualSHA256 string    `json:"actual_sha256"`
}

func ValidSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func ValidToken(value string) bool {
	if len(value) < 32 || len(value) > 256 {
		return false
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return false
		}
	}
	return true
}
