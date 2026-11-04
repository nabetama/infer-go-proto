// Package grpcsvc implements UserService handlers with grpc-go.
package grpcsvc

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/uptrace/bun"

	userv1 "github.com/nabetama/infer-go-proto/examples/gen/user/v1"
)

// Server serves SearchUsers.
type Server struct {
	db    *bun.DB
	sqlDB *sql.DB
}

// New returns a Server.
func New(db *bun.DB, sqlDB *sql.DB) *Server {
	return &Server{db: db, sqlDB: sqlDB}
}

// SearchUsersBad formats the requested order into the SQL string.
//
// want: TAINT_ERROR
func (s *Server) SearchUsersBad(ctx context.Context, req *userv1.SearchUsersRequest) (*userv1.SearchUsersResponse, error) {
	var names []string
	q := fmt.Sprintf("SELECT name FROM users ORDER BY %s", req.OrderBy)
	if err := s.db.NewRaw(q).Scan(ctx, &names); err != nil {
		return nil, err
	}
	return &userv1.SearchUsersResponse{Names: names}, nil
}

// SearchUsersGood passes the request's value as a placeholder argument.
//
// want: none
func (s *Server) SearchUsersGood(ctx context.Context, req *userv1.SearchUsersRequest) (*userv1.SearchUsersResponse, error) {
	var names []string
	if err := s.db.NewRaw("SELECT name FROM users WHERE name = ?", req.OrderBy).Scan(ctx, &names); err != nil {
		return nil, err
	}
	return &userv1.SearchUsersResponse{Names: names}, nil
}

// SearchUsersSafeBad marks the requested order as trusted SQL with bun.Safe.
//
// want: TAINT_ERROR
func (s *Server) SearchUsersSafeBad(ctx context.Context, req *userv1.SearchUsersRequest) (*userv1.SearchUsersResponse, error) {
	var names []string
	err := s.db.NewSelect().
		Table("users").
		Column("name").
		OrderExpr(string(bun.Safe(req.OrderBy))).
		Scan(ctx, &names)
	if err != nil {
		return nil, err
	}
	return &userv1.SearchUsersResponse{Names: names}, nil
}

// SearchUsersSafeArgBad passes the requested name as a placeholder
// argument, but wrapped in bun.Safe, so bun writes it into the SQL string
// without quoting. Only the bun.Safe conversion is a sink here: the query
// string given to Where is a constant.
//
// want: TAINT_ERROR
func (s *Server) SearchUsersSafeArgBad(ctx context.Context, req *userv1.SearchUsersRequest) (*userv1.SearchUsersResponse, error) {
	var names []string
	err := s.db.NewSelect().
		Table("users").
		Column("name").
		Where("name = ?", bun.Safe(req.OrderBy)).
		Scan(ctx, &names)
	if err != nil {
		return nil, err
	}
	return &userv1.SearchUsersResponse{Names: names}, nil
}

// SearchUsersSQLBad concatenates the requested order into the SQL string
// given to database/sql.
//
// want: TAINT_ERROR
func (s *Server) SearchUsersSQLBad(ctx context.Context, req *userv1.SearchUsersRequest) (*userv1.SearchUsersResponse, error) {
	rows, err := s.sqlDB.QueryContext(ctx, "SELECT name FROM users ORDER BY "+req.OrderBy)
	if err != nil {
		return nil, err
	}
	return scanNames(rows)
}

// SearchUsersSQLGood passes the request's value as a placeholder argument
// to database/sql.
//
// want: none
func (s *Server) SearchUsersSQLGood(ctx context.Context, req *userv1.SearchUsersRequest) (*userv1.SearchUsersResponse, error) {
	rows, err := s.sqlDB.QueryContext(ctx, "SELECT name FROM users WHERE name = ?", req.OrderBy)
	if err != nil {
		return nil, err
	}
	return scanNames(rows)
}

// scanNames returns the first name in rows.
func scanNames(rows *sql.Rows) (*userv1.SearchUsersResponse, error) {
	var name string
	if rows.Next() {
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return &userv1.SearchUsersResponse{Names: []string{name}}, nil
}
