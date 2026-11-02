// Package grpcsvc implements UserService handlers with grpc-go.
package grpcsvc

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"

	userv1 "github.com/nabetama/infer-go-proto/examples/gen/user/v1"
)

// Server serves SearchUsers.
type Server struct {
	db *bun.DB
}

// New returns a Server.
func New(db *bun.DB) *Server {
	return &Server{db: db}
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
