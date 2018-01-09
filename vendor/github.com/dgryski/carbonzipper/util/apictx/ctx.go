package apictx

import (
	"context"
	"net/http"
)

type key int

const (
	ctxHeaderUUID = "X-CTX-CarbonAPI-UUID"

	uuidKey key = 0
)

func ifaceToString(v interface{}) string {
	if v != nil {
		return v.(string)
	}
	return ""
}

func getCtxString(ctx context.Context, k key) string {
	return ifaceToString(ctx.Value(k))
}

func GetUUID(ctx context.Context) string {
	return getCtxString(ctx, uuidKey)
}

func SetUUID(ctx context.Context, v string) context.Context {
	return context.WithValue(ctx, uuidKey, v)
}

func ParseCtx(h http.HandlerFunc) http.HandlerFunc {
	return http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		uuid := req.Header.Get(ctxHeaderUUID)

		ctx := req.Context()
		ctx = SetUUID(ctx, uuid)

		h.ServeHTTP(rw, req.WithContext(ctx))
	})
}

func MarshalCtx(ctx context.Context, response *http.Request) *http.Request {
	response.Header.Add(ctxHeaderUUID, GetUUID(ctx))

	return response
}