package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt"
	"github.com/golang-jwt/jwt/request"
	"github.com/signal18/replication-manager/auth/user"
)

// IssueJWT issues a new JWT token for the user.
func IssueJWT(user *user.User, timeout int, signingKey []byte) (string, error) {
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
