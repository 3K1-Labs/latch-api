package service

import (
	"database/sql"

	_ "github.com/jackc/pgx/v5/stdlib"
	db "github.com/latch/backend/internal/db/generated"
)

// errorQueries returns a Queries backed by a closed database/sql.DB.
// All query methods will fail immediately with "sql: database is closed".
// This lets us exercise error-handling branches in service methods without
// a real database.
func errorQueries() *db.Queries {
	sqlDB, _ := sql.Open("pgx", "postgres://x:x@localhost:54999/x")
	sqlDB.Close()
	return db.New(sqlDB)
}

// errorDB returns a closed *sql.DB for testing error paths that require the
// raw *sql.DB (e.g. BeginTx).
func errorDB() *sql.DB {
	sqlDB, _ := sql.Open("pgx", "postgres://x:x@localhost:54999/x")
	sqlDB.Close()
	return sqlDB
}
