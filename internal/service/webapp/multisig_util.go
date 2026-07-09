package webapp

import (
	"database/sql"

	"github.com/google/uuid"
)

// nullStr wraps s as a valid sql.NullString, or an invalid (NULL) one if s
// is empty — used throughout the multisig services for optional per-kind
// member fields (gAddress, keyDataHex, credentialId, publicKeyHex, label).
func nullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// strOrEmpty unwraps a sql.NullString to "" when not valid.
func strOrEmpty(n sql.NullString) string {
	if !n.Valid {
		return ""
	}
	return n.String
}

// nullInt64OrEmpty unwraps a sql.NullInt64 into a *int64, nil when not valid.
func nullInt64Ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// nullUUID parses s as a valid uuid.NullUUID, or an invalid (NULL) one if s
// is empty or not a well-formed UUID.
func nullUUID(s string) uuid.NullUUID {
	if s == "" {
		return uuid.NullUUID{}
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: id, Valid: true}
}

// uuidOrEmpty unwraps a uuid.NullUUID to "" when not valid.
func uuidOrEmpty(n uuid.NullUUID) string {
	if !n.Valid {
		return ""
	}
	return n.UUID.String()
}
