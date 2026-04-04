// Package db provides database initialization and the shared *gorm.DB handle.
package db

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/c0d3d3v/streamer-2/internal/config"
	"github.com/glebarez/sqlite" // pure-Go SQLite driver (no CGO required)
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB is the application-wide GORM database handle. It is set by Init and
// then used directly by repository functions throughout the application.
var DB *gorm.DB

// Init opens the database connection according to the current configuration,
// enables WAL mode for SQLite (better concurrent read performance), and runs
// AutoMigrate to create or update all tables.
//
// models is a slice of GORM model pointers, e.g.:
//
//	db.Init(&stream.Stream{}, &archive.Archive{}, ...)
func Init(models ...interface{}) error {
	cfg := config.Get()

	gormCfg := &gorm.Config{
		// Silence routine query logs in production; only log warnings and errors.
		Logger: logger.Default.LogMode(logger.Warn),
	}

	var err error
	switch cfg.Database.Driver {
	case "sqlite", "":
		DB, err = openSQLite(cfg.Database.DSN, gormCfg)
	case "postgres":
		DB, err = openPostgres(cfg.Database.DSN, gormCfg)
	default:
		return fmt.Errorf("unsupported database driver %q (use 'sqlite' or 'postgres')", cfg.Database.Driver)
	}
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	// Run schema migrations for all registered models.
	if err := DB.AutoMigrate(models...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}

	log.Printf("database: using %s driver", cfg.Database.Driver)
	return nil
}

// openSQLite opens (or creates) the SQLite file at dsn, ensuring the parent
// directory exists. It also enables WAL journal mode so that readers do not
// block the writer.
func openSQLite(dsn string, cfg *gorm.Config) (*gorm.DB, error) {
	// Ensure the parent directory exists (e.g. "data/" may not exist on the first run).
	dir := filepath.Dir(dsn)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create database directory %s: %w", dir, err)
	}

	db, err := gorm.Open(sqlite.Open(dsn), cfg)
	if err != nil {
		return nil, err
	}

	// Enable WAL mode for better concurrent read / write performance.
	if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	// Foreign key enforcement is off by default in SQLite.
	if err := db.Exec("PRAGMA foreign_keys=ON").Error; err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	return db, nil
}

// openPostgres opens a PostgreSQL connection using the provided DSN.
func openPostgres(dsn string, cfg *gorm.Config) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), cfg)
}
