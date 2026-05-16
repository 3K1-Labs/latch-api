package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestHorizonService(httpClient *http.Client) *HorizonService {
	svc := NewHorizonService()
	svc.httpClient = httpClient
	return svc
}

func TestHorizonService_GetAccountOperations_Payment(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify query parameters are set correctly.
		q := r.URL.Query()
		if q.Get("order") != "desc" {
			t.Errorf("order = %q, want desc", q.Get("order"))
		}
		if q.Get("include_failed") != "true" {
			t.Errorf("include_failed = %q, want true", q.Get("include_failed"))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"_embedded": map[string]any{
				"records": []map[string]any{
					{
						"id":                      "op-1",
						"transaction_hash":        "txhash1",
						"transaction_successful":  true,
						"type":                    "payment",
						"created_at":              "2024-06-01T12:00:00Z",
						"from":                    "GFROM",
						"to":                      "GTO",
						"amount":                  "50.0000000",
						"asset_type":              "native",
					},
				},
			},
		})
	}))
	defer ts.Close()

	svc := newTestHorizonService(ts.Client())
	ops, err := svc.GetAccountOperations(context.Background(), ts.URL, "GTEST", 10)
	if err != nil {
		t.Fatalf("GetAccountOperations: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d operations, want 1", len(ops))
	}
	op := ops[0]
	if op.ID != "op-1" {
		t.Errorf("ID = %q, want op-1", op.ID)
	}
	if op.Type != "payment" {
		t.Errorf("Type = %q, want payment", op.Type)
	}
	if op.Amount != "50.0000000" {
		t.Errorf("Amount = %q, want 50.0000000", op.Amount)
	}
	if op.AssetType != "native" {
		t.Errorf("AssetType = %q, want native", op.AssetType)
	}
	if !op.TransactionSuccessful {
		t.Error("TransactionSuccessful should be true")
	}
}

func TestHorizonService_GetAccountOperations_NotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	svc := newTestHorizonService(ts.Client())
	ops, err := svc.GetAccountOperations(context.Background(), ts.URL, "GNEW", 10)
	if err != nil {
		t.Fatalf("404 should return nil error, got: %v", err)
	}
	if ops != nil {
		t.Errorf("404 should return nil ops, got %v", ops)
	}
}

func TestHorizonService_GetAccountOperations_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	svc := newTestHorizonService(ts.Client())
	_, err := svc.GetAccountOperations(context.Background(), ts.URL, "GTEST", 10)
	if err == nil {
		t.Error("expected error for 500 response, got nil")
	}
}

func TestHorizonService_GetAccountOperations_LimitCapped(t *testing.T) {
	var capturedLimit string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"_embedded": map[string]any{"records": []any{}},
		})
	}))
	defer ts.Close()

	svc := newTestHorizonService(ts.Client())

	// Limit > 200 should be capped to 50.
	_, _ = svc.GetAccountOperations(context.Background(), ts.URL, "GTEST", 500)
	if capturedLimit != "50" {
		t.Errorf("limit > 200 should be capped to 50, got %q", capturedLimit)
	}
}

func TestHorizonService_GetAccountOperations_CreateAccount(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"_embedded": map[string]any{
				"records": []map[string]any{
					{
						"id":                     "op-2",
						"transaction_hash":       "txhash2",
						"transaction_successful": true,
						"type":                   "create_account",
						"created_at":             "2024-01-01T00:00:00Z",
						"account":                "GNEWACCOUNT",
						"funder":                 "GFUNDER",
						"starting_balance":       "100.0000000",
					},
				},
			},
		})
	}))
	defer ts.Close()

	svc := newTestHorizonService(ts.Client())
	ops, err := svc.GetAccountOperations(context.Background(), ts.URL, "GNEWACCOUNT", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
	op := ops[0]
	if op.Account != "GNEWACCOUNT" {
		t.Errorf("Account = %q, want GNEWACCOUNT", op.Account)
	}
	if op.Funder != "GFUNDER" {
		t.Errorf("Funder = %q, want GFUNDER", op.Funder)
	}
	if op.StartingBalance != "100.0000000" {
		t.Errorf("StartingBalance = %q, want 100.0000000", op.StartingBalance)
	}
}
