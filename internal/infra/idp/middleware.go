package idp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type Identity struct {
	Subject  string
	ClientID string
	Roles    []string
}

func (id Identity) HasRole(role string) bool {
	for _, r := range id.Roles {
		if r == role {
			return true
		}
	}
	return false
}

type identityCtxKey struct{}

func IdentityFromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityCtxKey{}).(Identity)
	return id, ok
}

type claims struct {
	jwt.RegisteredClaims
	AuthorizedParty string      `json:"azp"`
	RealmAccess     realmAccess `json:"realm_access"`
}

type realmAccess struct {
	Roles []string `json:"roles"`
}

// Authenticate é o middleware que valida o Bearer JWT em toda
// requisição e injeta a Identity resultante no context.
func Authenticate(keySet *KeySet, issuer string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := extractBearerToken(r)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, err.Error())
				return
			}

			parsed, err := jwt.ParseWithClaims(token, &claims{}, func(t *jwt.Token) (any, error) {
				kid, ok := t.Header["kid"].(string)
				if !ok || kid == "" {
					return nil, errors.New("token sem kid no header")
				}
				return keySet.Key(kid)
			}, jwt.WithIssuer(issuer), jwt.WithValidMethods([]string{"RS256"}))

			if err != nil || !parsed.Valid {
				writeAuthError(w, http.StatusUnauthorized, "token inválido ou expirado")
				return
			}

			c, ok := parsed.Claims.(*claims)
			if !ok {
				writeAuthError(w, http.StatusUnauthorized, "claims inesperadas")
				return
			}

			identity := Identity{
				Subject:  c.Subject,
				ClientID: c.AuthorizedParty,
				Roles:    c.RealmAccess.Roles,
			}

			ctx := context.WithValue(r.Context(), identityCtxKey{}, identity)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity, ok := IdentityFromContext(r.Context())
			if !ok || !identity.HasRole(role) {
				writeAuthError(w, http.StatusForbidden, fmt.Sprintf("requer a role %q", role))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractBearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", errors.New("header Authorization ausente")
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", errors.New("header Authorization deve usar o esquema Bearer")
	}
	token := strings.TrimPrefix(header, prefix)
	if token == "" {
		return "", errors.New("token vazio")
	}
	return token, nil
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": http.StatusText(status), "message": message})
}
