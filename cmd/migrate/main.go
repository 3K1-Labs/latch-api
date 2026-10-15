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

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/joho/godotenv"
	"github.com/latch/backend/internal/deployment"
	"github.com/latch/backend/migrations"
)

func main() {
	// .env is optional in production
	_ = godotenv.Load()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL is required")
	}

	// The same embedded migrations the server applies at startup.
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		log.Fatalf("open embedded migrations: %v", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, deployment.ToPgx5URL(dbURL))
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
				log.Fatalf("down: N must be a positive integer, got %q", os.Args[2])
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
			log.Fatalf("force: invalid version %q", os.Args[2])
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

func printUsage() {
	fmt.Println(`usage: migrate <command> [args]

commands:
  up           apply all pending migrations
  down [N]     roll back N steps (default 1)
  version      print current migration version
  force <V>    force-set version V (recover from dirty state)`)
}
