package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// HorizonService fetches data from a Stellar Horizon REST API.
// The Horizon URL is passed per method to support testnet and mainnet.
type HorizonService struct {
	httpClient *http.Client
}

func NewHorizonService() *HorizonService {
	return &HorizonService{
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// HorizonOperation represents a single operation record from Horizon.
// Fields vary by type; missing fields are zero values.
type HorizonOperation struct {
	ID                    string `json:"id"`
	TransactionHash       string `json:"transaction_hash"`
	TransactionSuccessful bool   `json:"transaction_successful"`
	Type                  string `json:"type"`
	CreatedAt             string `json:"created_at"`

	// payment / path_payment fields
	From        string `json:"from,omitempty"`
	To          string `json:"to,omitempty"`
	Amount      string `json:"amount,omitempty"`
	AssetType   string `json:"asset_type,omitempty"`
	AssetCode   string `json:"asset_code,omitempty"`
	AssetIssuer string `json:"asset_issuer,omitempty"`

	// create_account fields
	Account         string `json:"account,omitempty"`
	Funder          string `json:"funder,omitempty"`
	StartingBalance string `json:"starting_balance,omitempty"`
}

type horizonOperationsResponse struct {
	Embedded struct {
		Records []HorizonOperation `json:"records"`
	} `json:"_embedded"`
}

// GetAccountOperations returns the most recent operations for a Stellar address.
// limit is capped at 200 (Horizon's maximum per page).
func (s *HorizonService) GetAccountOperations(ctx context.Context, horizonURL, address string, limit int) ([]HorizonOperation, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	endpoint, err := url.JoinPath(horizonURL, "accounts", address, "operations")
	if err != nil {
		return nil, fmt.Errorf("build url: %w", err)
	}

	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	q := u.Query()
	q.Set("order", "desc")
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("include_failed", "true")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("horizon request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Account not yet funded / doesn't exist on this network
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("horizon returned %d for %s", resp.StatusCode, address)
	}

	var result horizonOperationsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result.Embedded.Records, nil
}
