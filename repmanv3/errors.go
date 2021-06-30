package repmanv3

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrClusterNotFound = errors.New("cluster not found")
)

func NewErrorResource(code codes.Code, reason error, field string, contents string) *status.Status {
	st := status.New(code, reason.Error())
	st, err := st.WithDetails(&ErrorInfo{
		Reason: reason.Error(),
		Resource: &ErrorResource{
			Field:    field,
			Contents: contents,
		},
	})

	// this should never happen but just in case
	if err != nil {
		return status.Newf(code, "%s, for field %s; contents %s", reason, field, contents)
	}

	return st
}
