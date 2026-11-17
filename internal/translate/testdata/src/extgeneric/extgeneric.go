package extgeneric

import (
	"connectrpc.com/connect"

	"github.com/nabetama/infer-go-proto/internal/translate/testdata/src/structs"
)

func Handle(req *connect.Request[structs.User]) string {
	return req.Msg.Name
}
