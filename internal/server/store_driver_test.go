package server

import (
	"database/sql"
	"slices"
	"testing"
)

func TestPostgresDriverRegistered(t *testing.T) {
	if !slices.Contains(sql.Drivers(), "pgx") {
		t.Fatal("pgx database/sql driver is not registered")
	}
}
