package service

import (
	"encoding/base64"
	"testing"
	"time"
)

// buildI128XDR encodes a uint64 stroops value as a base64 SCV_I128 ScVal
// with hi=0 (covers all practical XLM amounts).
func buildI128XDR(lo uint64) string {
	raw := make([]byte, 20)
	raw[3] = 0x0A // SCV_I128
	for i := 0; i < 8; i++ {
		raw[19-i] = byte(lo >> (8 * uint(i)))
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestHorizonOpToTx_Payment(t *testing.T) {
	op := HorizonOperation{
		ID:                    "op-pay-1",
		TransactionHash:       "txhash1",
		TransactionSuccessful: true,
		Type:                  "payment",
		CreatedAt:             "2024-06-01T12:00:00Z",
		From:                  "GFROM",
		To:                    "GTO",
		Amount:                "25.0000000",
		AssetType:             "native",
	}

	tx := horizonOpToTx(op)

	if tx.ID != "op-pay-1" {
		t.Errorf("ID = %q, want op-pay-1", tx.ID)
	}
	if tx.Hash != "txhash1" {
		t.Errorf("Hash = %q, want txhash1", tx.Hash)
	}
	if tx.Type != "payment" {
		t.Errorf("Type = %q, want payment", tx.Type)
	}
	if tx.Status != "success" {
		t.Errorf("Status = %q, want success", tx.Status)
	}
	if tx.From != "GFROM" {
		t.Errorf("From = %q, want GFROM", tx.From)
	}
	if tx.To != "GTO" {
		t.Errorf("To = %q, want GTO", tx.To)
	}
	if tx.Amount != "25.0000000" {
		t.Errorf("Amount = %q, want 25.0000000", tx.Amount)
	}
	if tx.Asset != "native" {
		t.Errorf("Asset = %q, want native", tx.Asset)
	}

	want := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	if !tx.CreatedAt.Equal(want) {
		t.Errorf("CreatedAt = %v, want %v", tx.CreatedAt, want)
	}
}

func TestHorizonOpToTx_CreateAccount(t *testing.T) {
	op := HorizonOperation{
		ID:                    "op-ca-1",
		TransactionHash:       "txhash2",
		TransactionSuccessful: true,
		Type:                  "create_account",
		CreatedAt:             "2024-01-15T08:30:00Z",
		Account:               "GNEWACCT",
		Funder:                "GFUNDER",
		StartingBalance:       "1000.0000000",
	}

	tx := horizonOpToTx(op)

	if tx.From != "GFUNDER" {
		t.Errorf("From = %q, want GFUNDER", tx.From)
	}
	if tx.To != "GNEWACCT" {
		t.Errorf("To = %q, want GNEWACCT", tx.To)
	}
	if tx.Amount != "1000.0000000" {
		t.Errorf("Amount = %q, want 1000.0000000", tx.Amount)
	}
	if tx.Asset != "native" {
		t.Errorf("Asset = %q, want native for create_account", tx.Asset)
	}
}

func TestHorizonOpToTx_Failed(t *testing.T) {
	op := HorizonOperation{
		ID:                    "op-fail-1",
		TransactionHash:       "txhash3",
		TransactionSuccessful: false,
		Type:                  "payment",
		CreatedAt:             "2024-01-01T00:00:00Z",
	}
	tx := horizonOpToTx(op)
	if tx.Status != "failed" {
		t.Errorf("Status = %q, want failed", tx.Status)
	}
}

func TestHorizonOpToTx_InvokeHostFunction(t *testing.T) {
	op := HorizonOperation{
		ID:                    "op-ihf-1",
		TransactionHash:       "txhash4",
		TransactionSuccessful: true,
		Type:                  "invoke_host_function",
		CreatedAt:             "2024-01-01T00:00:00Z",
	}
	tx := horizonOpToTx(op)
	if tx.Amount != "" {
		t.Errorf("invoke_host_function should have empty Amount, got %q", tx.Amount)
	}
	if tx.From != "" {
		t.Errorf("invoke_host_function should have empty From, got %q", tx.From)
	}
}

// TestSacEventToTx verifies that a SAC event with encoded address topics
// decodes to the correct Transaction.
func TestSacEventToTx(t *testing.T) {
	fromPayload := make([]byte, 32)
	fromPayload[0] = 0x10
	toPayload := make([]byte, 32)
	toPayload[0] = 0x20

	fromAddr := encodeStrkey(strkeyVersionAccount, fromPayload)
	toAddr := encodeStrkey(strkeyVersionAccount, toPayload)

	fromXDR, err := AddressToScValXDR(fromAddr)
	if err != nil {
		t.Fatalf("AddressToScValXDR from: %v", err)
	}
	toXDR, err := AddressToScValXDR(toAddr)
	if err != nil {
		t.Fatalf("AddressToScValXDR to: %v", err)
	}

	ev := SorobanEvent{
		ID:                       "0000000000000001-000000001",
		TxHash:                   "sactxhash1",
		LedgerClosedAt:           "2024-07-04T00:00:00Z",
		InSuccessfulContractCall: true,
		Topic: []string{
			SymbolToScValXDR("transfer"),
			fromXDR,
			toXDR,
		},
		Value: buildI128XDR(50_000_000), // 5 XLM
	}

	tx := sacEventToTx(ev)

	if tx.Hash != "sactxhash1" {
		t.Errorf("Hash = %q, want sactxhash1", tx.Hash)
	}
	if tx.Type != "sac_transfer" {
		t.Errorf("Type = %q, want sac_transfer", tx.Type)
	}
	if tx.Status != "success" {
		t.Errorf("Status = %q, want success", tx.Status)
	}
	if tx.Asset != "native" {
		t.Errorf("Asset = %q, want native", tx.Asset)
	}
	if tx.From != fromAddr {
		t.Errorf("From = %q, want %q", tx.From, fromAddr)
	}
	if tx.To != toAddr {
		t.Errorf("To = %q, want %q", tx.To, toAddr)
	}
	if tx.Amount != "5.0000000" {
		t.Errorf("Amount = %q, want 5.0000000", tx.Amount)
	}

	wantTime := time.Date(2024, 7, 4, 0, 0, 0, 0, time.UTC)
	if !tx.CreatedAt.Equal(wantTime) {
		t.Errorf("CreatedAt = %v, want %v", tx.CreatedAt, wantTime)
	}
}

// TestSacEventToTx_NoHash verifies that a SAC event with an empty TxHash
// falls back to the event ID as the hash.
func TestSacEventToTx_NoHash(t *testing.T) {
	ev := SorobanEvent{
		ID:                       "1234-5678",
		TxHash:                   "",
		LedgerClosedAt:           "2024-01-01T00:00:00Z",
		InSuccessfulContractCall: true,
	}
	tx := sacEventToTx(ev)
	if tx.Hash == "" {
		t.Error("Hash should fall back to event ID when TxHash is empty")
	}
}

// TestAssetString covers all asset type formatting paths.
func TestAssetString(t *testing.T) {
	tests := []struct {
		assetType string
		code      string
		issuer    string
		want      string
	}{
		{"native", "", "", "native"},
		{"credit_alphanum4", "USDC", "GISSUER", "USDC:GISSUER"},
		{"credit_alphanum12", "LONGTOKEN", "GISSUER2", "LONGTOKEN:GISSUER2"},
		{"credit_alphanum4", "USDC", "", "USDC"},
	}
	for _, tt := range tests {
		got := assetString(tt.assetType, tt.code, tt.issuer)
		if got != tt.want {
			t.Errorf("assetString(%q, %q, %q) = %q, want %q",
				tt.assetType, tt.code, tt.issuer, got, tt.want)
		}
	}
}
