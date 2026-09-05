// cmd/migrate — thin CLI wrapper around golang-migrate.
//
// Usage:
//
//	go run ./cmd/migrate up          # apply all pending migrations
//	go run ./cmd/migrate down        # roll back one step
//	go run ./cmd/migrate down N      # roll back N steps
//	go run ./cmd/migrate version     # print current version
//	go run ./cmd/migrate force V     # force-set version (recover from dirty state)
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/joho/godotenv"
)

func main() {
	// .env is optional in production
	_ = godotenv.Load()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	// golang-migrate pgx/v5 driver requires the pgx5:// scheme
	migrateURL := toPgx5URL(dbURL)

	m, err := migrate.New("file://migrations", migrateURL)
	if err != nil {
		log.Fatalf("initialise migrate: %v", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			log.Printf("close source: %v", srcErr)
		}
		if dbErr != nil {
			log.Printf("close db: %v", dbErr)
		}
	}()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "up":
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			log.Fatalf("migrate up: %v", err)
		}
		v, _, _ := m.Version()
		fmt.Printf("migrations applied — now at version %d\n", v)

	case "down":
		steps := 1
		if len(os.Args) > 2 {
			n, err := strconv.Atoi(os.Args[2])
			if err != nil || n < 1 {
				// #nosec G706 -- sanitizeLogArg strips CR/LF before the argument is echoed, so no log line can be forged
				log.Fatalf("down: N must be a positive integer, got %q", sanitizeLogArg(os.Args[2]))
			}
			steps = n
		}
		if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			log.Fatalf("migrate down: %v", err)
		}
		v, _, _ := m.Version()
		fmt.Printf("rolled back %d step(s) — now at version %d\n", steps, v)

	case "version":
		v, dirty, err := m.Version()
		if errors.Is(err, migrate.ErrNilVersion) {
			fmt.Println("no migrations applied yet (version 0)")
			return
		}
		if err != nil {
			log.Fatalf("get version: %v", err)
		}
		dirtyStr := ""
		if dirty {
			dirtyStr = " (DIRTY — run 'migrate force <version>' to recover)"
		}
		fmt.Printf("version: %d%s\n", v, dirtyStr)

	case "force":
		if len(os.Args) < 3 {
			log.Fatal("force: version argument required")
		}
		v, err := strconv.Atoi(os.Args[2])
		if err != nil {
			// #nosec G706 -- sanitizeLogArg strips CR/LF before the argument is echoed, so no log line can be forged
			log.Fatalf("force: invalid version %q", sanitizeLogArg(os.Args[2]))
		}
		if err := m.Force(v); err != nil {
			log.Fatalf("force version: %v", err)
		}
		fmt.Printf("forced version to %d\n", v)

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %q\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// sanitizeLogArg strips CR/LF control characters from a user-supplied CLI
// argument before it is echoed into a log message, preventing CRLF log
// injection from forging additional log lines.
func sanitizeLogArg(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, s)
}

// toPgx5URL converts a standard postgres:// URL to the pgx5:// scheme
// required by golang-migrate's pgx/v5 database driver.
func toPgx5URL(url string) string {
	for _, prefix := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(url, prefix) {
			return "pgx5://" + url[len(prefix):]
		}
	}
	return url // already pgx5:// or something else
}

func printUsage() {
	fmt.Println(`usage: migrate <command> [args]

commands:
  up           apply all pending migrations
  down [N]     roll back N steps (default 1)
  version      print current migration version
  force <V>    force-set version V (recover from dirty state)`)
}
