// artex-audit-runner consumes offline artifact verification jobs from ARTEX.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Autumn-27/artex/auditjob"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

func main() {
	endpoint := flag.String("url", os.Getenv("ARTEX_AUDIT_URL"), "ARTEX origin (HTTPS, or HTTP on loopback)")
	concurrency := flag.Int("concurrency", 3, "parallel audit jobs (1–32)")
	flag.Parse()
	if *concurrency < 1 || *concurrency > 32 {
		slog.Error("concurrency must be between 1 and 32")
		os.Exit(2)
	}
	client, err := newRunnerClient(*endpoint, os.Getenv(auditjob.TokenEnv))
	if err != nil {
		slog.Error("invalid runner configuration", "error", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	group, ctx := errgroup.WithContext(ctx)
	for range *concurrency {
		group.Go(func() error { return client.run(ctx) })
	}
	if err := group.Wait(); err != nil {
		slog.Error("audit runner stopped", "error", err)
		os.Exit(1)
	}
}

type runnerClient struct {
	origin string
	token  string
	http   *http.Client
}

func newRunnerClient(endpoint, token string) (*runnerClient, error) {
	if !auditjob.ValidToken(token) {
		return nil, fmt.Errorf("%s must be a 32–256 character URL-safe secret", auditjob.TokenEnv)
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" ||
		(u.EscapedPath() != "" && u.EscapedPath() != "/") {
		return nil, errors.New("provide the ARTEX origin without path, query, or credentials")
	}
	loopback := u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback()
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, errors.New("ARTEX URL requires HTTPS outside loopback")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 32
	return &runnerClient{
		origin: u.Scheme + "://" + u.Host,
		token:  token,
		http: &http.Client{
			Transport: transport,
			Timeout:   8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *runnerClient) post(ctx context.Context, route string, payload []byte, claim *auditjob.Claim) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.origin+auditjob.RunnerPrefix+route, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	if len(payload) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	status := response.StatusCode
	if claim == nil || status != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return status, nil
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, auditjob.MaxJobJSON+1))
	if err != nil {
		return status, err
	}
	if len(body) > auditjob.MaxJobJSON {
		return status, errors.New("claim response above size limit")
	}
	if err := json.Unmarshal(body, claim); err != nil {
		return status, err
	}
	if claim.ID == uuid.Nil || claim.LeaseID == uuid.Nil || claim.Artifact == nil || len(claim.Artifact) > auditjob.MaxArtifact {
		return status, errors.New("invalid audit claim")
	}
	return status, nil
}

func fatalRunnerStatus(status int) bool {
	return status >= 300 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusTooManyRequests
}

func (c *runnerClient) complete(ctx context.Context, claim auditjob.Claim) error {
	digest := sha256.Sum256(claim.Artifact)
	payload, err := json.Marshal(auditjob.Completion{LeaseID: claim.LeaseID, ActualSHA256: hex.EncodeToString(digest[:])})
	if err != nil {
		return err
	}
	for range 2 {
		status, err := c.post(ctx, claim.ID.String()+"/complete", payload, nil)
		switch {
		case status == http.StatusNoContent, status == http.StatusConflict, ctx.Err() != nil:
			return nil
		case fatalRunnerStatus(status):
			return fmt.Errorf("audit completion rejected (HTTP %d)", status)
		default:
			slog.Warn("audit completion unavailable", "job_id", claim.ID, "http_status", status, "error", err)
		}
	}
	return nil // The server makes the unfinished claim available after its lease expires.
}

func (c *runnerClient) run(ctx context.Context) error {
	delay := 500 * time.Millisecond
	for ctx.Err() == nil {
		var claim auditjob.Claim
		status, err := c.post(ctx, "claim", nil, &claim)
		if status == http.StatusOK && err == nil {
			delay = 500 * time.Millisecond
			if err := c.complete(ctx, claim); err != nil {
				return err
			}
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		if fatalRunnerStatus(status) {
			return fmt.Errorf("audit claim rejected (HTTP %d)", status)
		}
		if status != http.StatusNoContent || err != nil {
			slog.Warn("audit queue unavailable", "http_status", status, "error", err)
		}
		jitter := time.Duration(rand.Int64N(int64(delay / 2)))
		timer := time.NewTimer(delay + jitter)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		delay = min(2*delay, 4*time.Second)
	}
	return nil
}
