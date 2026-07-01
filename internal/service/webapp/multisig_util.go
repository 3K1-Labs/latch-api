package webapp

import "database/sql"

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
