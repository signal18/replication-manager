package auth

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt"
	"github.com/golang-jwt/jwt/request"
	"github.com/signal18/replication-manager/auth/user"
)

type AuthTry struct {
	User string    `json:"username"`
	Try  int       `json:"try"`
	Time time.Time `json:"time"`
}

// Users will be stored here
type Auth struct {
	Attempts *AuthTryMap
	Users    *user.UserMap
}

func InitAuth() Auth {
	return Auth{
		Attempts: NewAuthTryMap(),
		Users:    user.NewUserMap(),
	}
}

func (auth *Auth) LogAttempt(user user.UserCredentials) (*AuthTry, error) {
	var auth_try *AuthTry

	if auth_try, ok := auth.Attempts.CheckAndGet(user.Username); ok {
		// If exceed 3 times
		if auth_try.Try == 3 {
			if time.Now().Before(auth_try.Time.Add(3 * time.Minute)) {
				return auth_try, fmt.Errorf("3 authentication errors for the user " + user.Username + ", please try again in 3 minutes")
			} else {
				auth_try.Try = 1
				auth_try.Time = time.Now()
			}
		} else {
			auth_try.Try += 1
		}
	} else {
		auth_try = new(AuthTry)
		auth_try.User = user.Username
		auth_try.Try = 1
		auth_try.Time = time.Now()
		auth.Attempts.Set(user.Username, auth_try)
	}

	return auth_try, nil
}

// IssueJWT issues a new JWT token for the user.
func (auth *Auth) IssueJWT(user *user.User, timeout int, signingKey []byte) (string, error) {
	signer := jwt.New(jwt.SigningMethodRS256)
	claims := signer.Claims.(jwt.MapClaims)
	//set claims
	claims["iss"] = "https://api.replication-manager.signal18.io"
	claims["iat"] = time.Now().Unix()
	claims["exp"] = time.Now().Add(time.Hour * time.Duration(timeout)).Unix()
	claims["jti"] = "1" // should be user ID(?)
	claims["CustomUserInfo"] = struct {
		Name     string
		Role     string
		Password string
	}{user.User, "Member", user.Password}
	signer.Claims = claims
	sk, _ := jwt.ParseRSAPrivateKeyFromPEM(signingKey)

	return signer.SignedString(sk)
}

func (auth *Auth) LoginAttempt(user user.UserCredentials) (*user.User, error) {
	auth_try, err := auth.LogAttempt(user)
	if err != nil {
		return nil, err
	}

	u, ok := auth.Users.CheckAndGet(user.Username)
	if !ok || user.Password != u.Password {
		return nil, fmt.Errorf("invalid credentials")
	}

	auth_try.User = user.Username
	auth_try.Try = 1
	auth_try.Time = time.Now()

	return u, nil
}

// ValidateJWT validates the JWT token and extracts claims.
func ValidateJWT(r *http.Request, verificationKey []byte) (jwt.MapClaims, error) {

	token, err := request.ParseFromRequest(r, request.AuthorizationHeaderExtractor, func(token *jwt.Token) (interface{}, error) {
		vk, _ := jwt.ParseRSAPublicKeyFromPEM(verificationKey)
		return vk, nil
	})

	if err != nil || !token.Valid {
		return nil, errors.New("invalid token")
	}

	return token.Claims.(jwt.MapClaims), nil
}
