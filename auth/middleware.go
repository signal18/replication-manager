package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/codegangsta/negroni"
	"github.com/signal18/replication-manager/auth/user"
)

// Key type to avoid conflicts in context
type contextKey string

// Key to store/retrieve user from context
const userContextKey = contextKey("user")

// PermissionType specifies the type of permission check
type PermissionType int

const (
	PublicPermission PermissionType = iota
	AuthPermission
	ServerPermission
	ClusterPermission
)

// CheckPermission ensures the user has the necessary permissions based on the permission type.
func CheckPermission(permission string, permissionType PermissionType, auth *Auth, OAuthProvider string) negroni.HandlerFunc {
	return negroni.HandlerFunc(func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
		// Read user if not public permission
		if permissionType != PublicPermission {
			// Get user from context or token
			u, err := GetUserFromJWT(r, auth.Users, auth.SecureKey.PublicKey, OAuthProvider)
			if err != nil {
				http.Error(w, "Unauthorized: user not found - "+err.Error(), http.StatusUnauthorized)
				return
			}

			// Check permissions based on the type
			// Auth Permission only check if user JWT is valid
			switch permissionType {
			case ServerPermission:
				if u.User != "admin" && !u.HasClusterPermission("Default", permission) {
					http.Error(w, "Forbidden: insufficient server permissions", http.StatusForbidden)
					return
				}
			case ClusterPermission:
				clusterID := strings.Split(strings.TrimPrefix(r.URL.Path, "/clusters/"), "/")[0]
				if !u.HasClusterPermission(clusterID, permission) {
					http.Error(w, "Forbidden: insufficient cluster permissions", http.StatusForbidden)
					return
				}
			}

			// Set user in context so we can reuse it later if needed
			ctx := context.WithValue(r.Context(), userContextKey, u)

			// Call the next handler with the updated context
			next(w, r.WithContext(ctx))
		}

		//Call the next handler
		next(w, r)
	})
}

// Helper to retrieve user from request context
func GetUserFromJWT(r *http.Request, usermap map[string]*user.User, verificationKey []byte, OAuthProvider string) (*user.User, error) {
	var u *user.User

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
			u, ok = usermap[meuser]
			if !ok {
				return nil, fmt.Errorf("User is not found in cluster")
			} else if mepwd != u.Password {
				return nil, fmt.Errorf("Wrong credentials in JWT")
			}
		} else {
			if !strings.Contains(profile.(string), OAuthProvider) {
				return nil, fmt.Errorf("Invalid OAuth provider in JWT")
			} else {
				if meuser, ok := mycutinfo["email"]; !ok {
					return nil, fmt.Errorf("Email is not found in JWT")
				} else {
					u, ok = usermap[meuser.(string)]
					if !ok {
						return nil, fmt.Errorf("User is not found")
					}
				}
			}
		}
	}

	return u, nil
}

// Retrieve the user from the request context
func GetUserFromRequest(r *http.Request) (*user.User, error) {
	user, ok := r.Context().Value(userContextKey).(*user.User)
	if !ok || user == nil {
		return nil, fmt.Errorf("user not found in context")
	}
	return user, nil
}
