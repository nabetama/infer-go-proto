// Package userrepo reads users with bun.
package userrepo

import (
	"context"

	"github.com/uptrace/bun"
)

// Repo reads the users table.
type Repo struct {
	db *bun.DB
}

// New returns a Repo that uses db.
func New(db *bun.DB) *Repo {
	return &Repo{db: db}
}

// FindByName builds the WHERE clause by concatenating name into the SQL.
// The caller decides whether name comes from outside.
func (r *Repo) FindByName(ctx context.Context, name string) (string, error) {
	var found string
	err := r.db.NewSelect().
		Table("users").
		Column("name").
		Where("name = '"+name+"'").
		Scan(ctx, &found)
	return found, err
}
