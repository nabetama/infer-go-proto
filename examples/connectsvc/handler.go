// Package connectsvc implements UserService handlers with connect.
package connectsvc

import (
	"context"

	"connectrpc.com/connect"
	"github.com/uptrace/bun"

	userv1 "github.com/nabetama/infer-go-proto/examples/gen/user/v1"
	"github.com/nabetama/infer-go-proto/examples/userrepo"
)

// Handler serves GetUser.
type Handler struct {
	db   *bun.DB
	repo *userrepo.Repo
}

// New returns a Handler.
func New(db *bun.DB) *Handler {
	return &Handler{db: db, repo: userrepo.New(db)}
}

// GetUserBad puts the request's name into the SQL string.
//
// want: TAINT_ERROR
func (h *Handler) GetUserBad(ctx context.Context, req *connect.Request[userv1.GetUserRequest]) (*connect.Response[userv1.GetUserResponse], error) {
	var name string
	err := h.db.NewSelect().
		Table("users").
		Column("name").
		Where("name = '"+req.Msg.GetName()+"'").
		Scan(ctx, &name)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&userv1.GetUserResponse{Name: name}), nil
}

// GetUserGood passes the request's name as a placeholder argument.
//
// want: none
func (h *Handler) GetUserGood(ctx context.Context, req *connect.Request[userv1.GetUserRequest]) (*connect.Response[userv1.GetUserResponse], error) {
	var name string
	err := h.db.NewSelect().
		Table("users").
		Column("name").
		Where("name = ?", req.Msg.GetName()).
		Scan(ctx, &name)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&userv1.GetUserResponse{Name: name}), nil
}

// GetUserViaRepoBad passes the request's name to a repository that puts it
// into the SQL string.
//
// want: TAINT_ERROR
func (h *Handler) GetUserViaRepoBad(ctx context.Context, req *connect.Request[userv1.GetUserRequest]) (*connect.Response[userv1.GetUserResponse], error) {
	name, err := h.repo.FindByName(ctx, req.Msg.Name)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&userv1.GetUserResponse{Name: name}), nil
}
