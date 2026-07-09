package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ErrRelayerNotConfigured is returned when RELAYER_URL is unset. Registration
// is best-effort infrastructure, not a hard dependency — callers should log
// and move on rather than fail the caller's own operation.
var ErrRelayerNotConfigured = errors.New("relayer url not configured")

// RelayerRegistration is the result of registering a C-address with
// latch-relayer for pooled-deposit memo routing.
type RelayerRegistration struct {
	MemoID      int64
	PoolAddress string
}

// RelayerService calls latch-relayer's registration API.
type RelayerService struct {
	baseURL    string
	httpClient *http.Client
}

func NewRelayerService(baseURL string, timeout time.Duration) *RelayerService {
	return &RelayerService{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
	}
}

type relayerRegisterRequest struct {
	CAddress string `json:"c_address"`
}

type relayerRegisterResponse struct {
	MemoID      string `json:"memo_id"`
	PoolAddress string `json:"pool_address"`
}

// Register calls latch-relayer's idempotent POST /register for cAddress.
// Calling it more than once for the same address is safe — the relayer
// returns the existing registration.
//
// memo_id is a uint64 on the relayer side, transmitted as a decimal string
// and stored here as a bit-preserving int64 cast, matching latch-relayer's
// own BIGINT column (see latch-relayer/migrations/001_init.up.sql).
func (s *RelayerService) Register(ctx context.Context, cAddress string) (RelayerRegistration, error) {
	if s.baseURL == "" {
		return RelayerRegistration{}, ErrRelayerNotConfigured
	}

	body, err := json.Marshal(relayerRegisterRequest{CAddress: cAddress})
	if err != nil {
		return RelayerRegistration{}, fmt.Errorf("marshal register request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/register", bytes.NewReader(body))
	if err != nil {
		return RelayerRegistration{}, fmt.Errorf("build register request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return RelayerRegistration{}, fmt.Errorf("call relayer register: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return RelayerRegistration{}, fmt.Errorf("relayer register: unexpected status %d", resp.StatusCode)
	}

	var out relayerRegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return RelayerRegistration{}, fmt.Errorf("decode register response: %w", err)
	}

	rawMemoID, err := strconv.ParseUint(out.MemoID, 10, 64)
	if err != nil {
		return RelayerRegistration{}, fmt.Errorf("parse memo_id %q: %w", out.MemoID, err)
	}

	return RelayerRegistration{MemoID: int64(rawMemoID), PoolAddress: out.PoolAddress}, nil
}
