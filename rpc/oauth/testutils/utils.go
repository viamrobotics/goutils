// Package testutils contains test helper methods for the rpc/oauth package
package testutils

import (
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v4"

	"go.viam.com/utils/rpc"
)

// SignWebAuthAccessToken returns an access jwt access token typically done by auth0 during access token flow.
func SignWebAuthAccessToken(key *rsa.PrivateKey, entity, aud, iss, keyID string) (string, error) {
	token := &jwt.Token{
		Header: map[string]interface{}{
			"typ": "JWT",
			"alg": jwt.SigningMethodRS256.Alg(),
			"kid": keyID,
		},
		Claims: rpc.JWTClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Audience: []string{aud},
				Issuer:   iss,
				// in prod this may not be 1:1 to the email. This is usually the user id from auth0. For testing ensure it does not
				// match the email of the the entity.
				Subject:  fmt.Sprintf("viam/%s", entity),
				IssuedAt: jwt.NewNumericDate(time.Now()),
			},
			AuthCredentialsType: rpc.CredentialsType("oauth-web-auth"), // avoid circular dependency
			AuthMetadata: map[string]string{
				"email": entity,
			},
		},
		Method: jwt.SigningMethodRS256,
	}

	return token.SignedString(key)
}
