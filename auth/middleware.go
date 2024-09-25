package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/signal18/replication-manager/auth/user"
)

// Key type to avoid conflicts in context
type contextKey string

// Key to store/retrieve user from context
const userContextKey = contextKey("user")

// CheckServerPermission ensures the user has the necessary server-level permission.
func CheckServerPermission(permission string, usermap *user.UserMap, verificationKey []byte, OAuthProvider string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get user from context or token
		user, err := GetUserFromJWT(r, usermap, verificationKey, OAuthProvider)
		if err != nil {
			http.Error(w, "Unauthorized: user not found", http.StatusUnauthorized)
			return
		}

		// Check for server-level permission
		if !user.HasClusterPermission("Default", permission) {
			http.Error(w, "Forbidden: insufficient server permissions", http.StatusForbidden)
			return
		}

		// Set user in context so we can reuse it later if needed
		ctx := context.WithValue(r.Context(), userContextKey, user)

		// Call the next handler with the updated context
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CheckClusterPermission ensures the user has the necessary cluster-level permission.
func CheckClusterPermission(permission string, usermap *user.UserMap, verificationKey []byte, OAuthProvider string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := GetUserFromJWT(r, usermap, verificationKey, OAuthProvider)
		if err != nil {
			http.Error(w, "Unauthorized: user not found", http.StatusUnauthorized)
			return
		}

		clusterID := strings.Split(strings.TrimPrefix(r.URL.Path, "/clusters/"), "/")[0]
		if !user.HasClusterPermission(clusterID, permission) {
			http.Error(w, "Forbidden: insufficient cluster permissions", http.StatusForbidden)
			return
		}

		// Set user in context so we can reuse it later if needed
		ctx := context.WithValue(r.Context(), userContextKey, user)

		// Call the next handler with the updated context
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Helper to retrieve user from request context
func GetUserFromJWT(r *http.Request, userMap *user.UserMap, verificationKey []byte, OAuthProvider string) (*user.User, error) {
	var user *user.User

	claims, err := ValidateJWT(r, verificationKey)
	if err != nil {
		return nil, err
	}

	if userinfo, ok := claims["CustomUserInfo"]; !ok {
		return nil, fmt.Errorf("User info is not found within JWT claims")
	} else {
		mycutinfo := userinfo.(map[string]interface{})
		// If OIDC
		if profile, ok := mycutinfo["profile"]; !ok {
			meuser := mycutinfo["Name"].(string)
			mepwd := mycutinfo["Password"].(string)
			user, ok = userMap.CheckAndGet(meuser)
			if !ok {
				return nil, fmt.Errorf("User is not found in cluster")
			} else if mepwd != user.Password {
				return nil, fmt.Errorf("Wrong credentials in JWT")
			}
		} else {
			if !strings.Contains(profile.(string), OAuthProvider) {
				return nil, fmt.Errorf("Invalid OAuth provider in JWT")
			} else {
				if meuser, ok := mycutinfo["email"]; !ok {
					return nil, fmt.Errorf("Email is not found in JWT")
				} else {
					user, ok = userMap.CheckAndGet(meuser.(string))
					if !ok {
						return nil, fmt.Errorf("User is not found")
					}
				}
			}
		}
	}

	return user, nil
}

// Retrieve the user from the request context
func GetUserFromRequest(r *http.Request) (*user.User, error) {
	user, ok := r.Context().Value(userContextKey).(*user.User)
	if !ok || user == nil {
		return nil, fmt.Errorf("user not found in context")
	}
	return user, nil
}
