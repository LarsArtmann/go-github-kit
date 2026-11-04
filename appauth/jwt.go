package appauth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json/v2"
	"encoding/pem"
	"fmt"
	"time"
)

// JWT lifetime constants per GitHub's App auth guidance.
const (
	// jwtTTL bounds one App JWT; GitHub caps validity at 10 minutes.
	jwtTTL = 10 * time.Minute
	// jwtBackdate absorbs clock skew between us and GitHub.
	jwtBackdate = 60 * time.Second
	// tokenRefreshLead re-mints installation tokens one minute before they
	// expire instead of racing the deadline.
	tokenRefreshLead = time.Minute
)

// AppAuth signs GitHub App JWTs with the app's RSA private key.
type AppAuth struct {
	appID      int64
	privateKey *rsa.PrivateKey
}

// NewAppAuth parses a GitHub App private key (PKCS#1 or PKCS#8 PEM, exactly
// what the App settings page downloads).
func NewAppAuth(appID int64, pemKey []byte) (*AppAuth, error) {
	if appID <= 0 {
		return nil, ErrAppIDRequired
	}

	block, _ := pem.Decode(pemKey)
	if block == nil {
		return nil, ErrInvalidPEM
	}

	var key *rsa.PrivateKey
	if parsed, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = parsed
	} else if parsedAny, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := parsedAny.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("%w: PKCS#8 key is %T, want RSA", ErrInvalidPEM, parsedAny)
		}

		key = rsaKey
	} else {
		return nil, fmt.Errorf("appauth: parse private key: %w", err)
	}

	return &AppAuth{appID: appID, privateKey: key}, nil
}

// Sign produces one App JWT. The result authenticates Apps-API requests
// for roughly [jwtTTL] minus the backdate; callers should re-sign per
// request batch (see [AppAuthTransport] which caches for you).
func (a *AppAuth) Sign(now time.Time) (string, error) {
	claims, err := json.Marshal(map[string]int64{
		"iat": now.Add(-jwtBackdate).Unix(),
		"exp": now.Add(jwtTTL).Unix(),
		"iss": a.appID,
	})
	if err != nil {
		return "", fmt.Errorf("appauth: encode claims: %w", err)
	}

	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", fmt.Errorf("appauth: encode header: %w", err)
	}

	encode := base64.RawURLEncoding.EncodeToString
	signingInput := encode(header) + "." + encode(claims)
	digest := sha256.Sum256([]byte(signingInput))

	signature, err := rsa.SignPKCS1v15(rand.Reader, a.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("appauth: sign: %w", err)
	}

	return signingInput + "." + encode(signature), nil
}

// jwtValidUntil reports when a JWT signed at now stops being safely usable
// (expiry minus a safety margin).
func jwtValidUntil(now time.Time) time.Time {
	return now.Add(jwtTTL - jwtBackdate - tokenRefreshLead)
}
