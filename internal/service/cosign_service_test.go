package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	validUID = "11111111-1111-1111-1111-111111111111"
	validRID = "22222222-2222-2222-2222-222222222222"
)

func TestNewCosignService(t *testing.T) {
	assert.NotNil(t, NewCosignService(nil))
}

// ── Create ──────────────────────────────────────────────────────────────────

func TestCosignServiceCreate_InvalidUser(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.Create(context.Background(), "not-a-uuid", CreateCosignInput{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestCosignServiceCreate_InsertError(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.Create(context.Background(), validUID, CreateCosignInput{
		SmartAccountAddress: "CABC", UnsignedTxXDR: "v1:x", Network: "testnet", Threshold: 2,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert cosign request")
}

// ── List ────────────────────────────────────────────────────────────────────

func TestCosignServiceList_InvalidUser(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.List(context.Background(), "bad", "CABC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestCosignServiceList_QueryError(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.List(context.Background(), validUID, "CABC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list cosign requests")
}

// ── Get / getOwned ──────────────────────────────────────────────────────────

func TestCosignServiceGet_InvalidUser(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.Get(context.Background(), "bad", validRID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestCosignServiceGet_MalformedID(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.Get(context.Background(), validUID, "not-a-uuid")
	require.ErrorIs(t, err, ErrCosignNotFound)
}

func TestCosignServiceGet_QueryError(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.Get(context.Background(), validUID, validRID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get cosign request")
}

// ── AddSignature ──────────────────────────────────────────────────────────────

func TestCosignServiceAddSignature_MalformedID(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.AddSignature(context.Background(), validUID, "not-a-uuid", "key", "xdr")
	require.ErrorIs(t, err, ErrCosignNotFound)
}

func TestCosignServiceAddSignature_QueryError(t *testing.T) {
	svc := NewCosignService(errorQueries())
	_, err := svc.AddSignature(context.Background(), validUID, validRID, "key", "xdr")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get cosign request")
}

// ── MarkSubmitted / Cancel ────────────────────────────────────────────────────

func TestCosignServiceMarkSubmitted_InvalidUser(t *testing.T) {
	svc := NewCosignService(errorQueries())
	err := svc.MarkSubmitted(context.Background(), "bad", validRID, "hash")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestCosignServiceMarkSubmitted_QueryError(t *testing.T) {
	svc := NewCosignService(errorQueries())
	err := svc.MarkSubmitted(context.Background(), validUID, validRID, "hash")
	require.Error(t, err)
}

func TestCosignServiceCancel_InvalidUser(t *testing.T) {
	svc := NewCosignService(errorQueries())
	err := svc.Cancel(context.Background(), "bad", validRID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse user id")
}

func TestCosignServiceCancel_QueryError(t *testing.T) {
	svc := NewCosignService(errorQueries())
	err := svc.Cancel(context.Background(), validUID, validRID)
	require.Error(t, err)
}
