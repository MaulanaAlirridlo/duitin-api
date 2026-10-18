package main

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func openDatabase() (*sql.DB, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsnURL := url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(os.Getenv("PGUSER"), os.Getenv("PGPASSWORD")),
			Host:   net.JoinHostPort(os.Getenv("PGHOST"), os.Getenv("PGPORT")),
			Path:   "/duitin",
		}
		query := url.Values{}
		query.Set("sslmode", os.Getenv("PGSSLMODE"))
		dsnURL.RawQuery = query.Encode()
		dsn = dsnURL.String()
	}

	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL configuration: %w", err)
	}
	config.Database = "duitin"
	if config.Password == "" {
		config.Password = os.Getenv("PGPASSWORD")
	}

	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL database %q: %w", config.Database, err)
	}
	return db, nil
}
