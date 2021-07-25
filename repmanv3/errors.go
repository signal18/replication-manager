package repmanv3

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrClusterNotSet   = errors.New("cluster name not set")
	ErrClusterNotFound = errors.New("cluster not found")
	ErrEnumNotSet      = errors.New("mandatory enum not set")
	ErrFieldNotSet     = errors.New("mandatory field not set")
	ErrServerNotFound  = errors.New("server not found")
	ErrUserNotGranted  = errors.New("user not granted permission for this action")
	ErrGrantNotFound   = errors.New("cluster grant not found")
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

func NewError(code codes.Code, reason error) *status.Status {
	st := status.New(code, reason.Error())
	st, err := st.WithDetails(&ErrorInfo{
		Reason: reason.Error(),
	})

	// this should never happen but just in case
	if err != nil {
		return status.Newf(code, "%s", reason)
	}

	return st
}
