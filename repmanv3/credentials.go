package repmanv3

import (
	context "context"

	"google.golang.org/grpc/metadata"
)

type LoginCreds struct {
	Username string
	Password string
}

func NewCredentials(username string, password string) *LoginCreds {
	return &LoginCreds{
		Username: username,
		Password: password,
	}
}

func (c *LoginCreds) Validate() bool {
	if c.Username == `` || c.Password == `` {
		return false
	}

	return true
}

func (c *LoginCreds) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{
		"username": c.Username,
		"password": c.Password,
	}, nil
}

// TODO: this has to be set to true
func (c *LoginCreds) RequireTransportSecurity() bool {
	return false
}

func CredentialsFromContext(ctx context.Context) *LoginCreds {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if len(md["username"]) > 0 && len(md["password"]) > 0 {
			return &LoginCreds{
				Username: md["username"][0],
				Password: md["password"][0],
			}
		}
	}

	return nil
}
