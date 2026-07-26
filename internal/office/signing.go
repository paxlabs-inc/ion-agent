package office

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func signFileTokenAt(
	docID, versionID uuid.UUID,
	secret string,
	now time.Time,
	ttl time.Duration,
) (string, error) {
	payload := fileTokenPayload{
		DocID:     docID.String(),
		VersionID: versionID.String(),
		Purpose:   "file-download",
		ExpiresAt: now.Add(ttl).Unix(),
	}
	return signPayload(payload, purposeKey(secret, payload.Purpose))
}

// signCallbackToken creates a signed token for save callback URLs.
func signCallbackToken(
	session EditorSession,
	secret string,
	now time.Time,
	ttl time.Duration,
) (string, error) {
	payload := callbackTokenPayload{
		SessionID:  session.ID.String(),
		ActorID:    session.ActorID.String(),
		DocumentID: session.DocumentID.String(),
		EngineKey:  session.EngineDocKey,
		Purpose:    "save-callback",
		ExpiresAt:  now.Add(ttl).Unix(),
	}
	return signPayload(payload, purposeKey(secret, payload.Purpose))
}

// verifyFileToken validates and extracts a file download token.
func verifyFileToken(
	token, secret string,
	now time.Time,
) (fileTokenPayload, error) {
	var payload fileTokenPayload
	if err := verifyAndExtract(
		token, purposeKey(secret, "file-download"), &payload,
	); err != nil {
		return payload, err
	}
	if payload.Purpose != "file-download" {
		return payload, fmt.Errorf("office: invalid token purpose")
	}
	if now.Unix() >= payload.ExpiresAt {
		return payload, fmt.Errorf("office: token expired")
	}
	return payload, nil
}

// verifyCallbackToken validates and extracts a callback token.
func verifyCallbackToken(
	token, secret string,
	now time.Time,
) (callbackTokenPayload, error) {
	var payload callbackTokenPayload
	if err := verifyAndExtract(
		token, purposeKey(secret, "save-callback"), &payload,
	); err != nil {
		return payload, err
	}
	if payload.Purpose != "save-callback" {
		return payload, fmt.Errorf("office: invalid token purpose")
	}
	if now.Unix() >= payload.ExpiresAt {
		return payload, fmt.Errorf("office: token expired")
	}
	return payload, nil
}

type fileTokenPayload struct {
	DocID     string `json:"d"`
	VersionID string `json:"v"`
	Purpose   string `json:"p"`
	ExpiresAt int64  `json:"e"`
}

type callbackTokenPayload struct {
	SessionID  string `json:"s"`
	ActorID    string `json:"a"`
	DocumentID string `json:"d"`
	EngineKey  string `json:"k"`
	Purpose    string `json:"p"`
	ExpiresAt  int64  `json:"e"`
}

func signPayload(payload any, secret string) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("office: marshal token: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encoded + "." + sig, nil
}

func verifyAndExtract(token, secret string, target any) error {
	if len(token) == 0 || len(token) > 4096 {
		return fmt.Errorf("office: malformed token")
	}
	dot := -1
	for i := len(token) - 1; i >= 0; i-- {
		if token[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		return fmt.Errorf("office: malformed token")
	}
	encoded := token[:dot]
	sig := token[dot+1:]
	if len(encoded) == 0 || len(encoded) > 3072 || len(sig) == 0 || len(sig) > 128 {
		return fmt.Errorf("office: malformed token")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return fmt.Errorf("office: invalid token signature")
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("office: decode token: %w", err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("office: unmarshal token: %w", err)
	}
	return nil
}

func purposeKey(secret, purpose string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("ion-office:" + purpose))
	return string(mac.Sum(nil))
}

func signEditorJWT(config EditorConfig, secret string) (string, error) {
	config.Token = ""
	payload, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("office: marshal editor token: %w", err)
	}
	return signJWTBytes(payload, secret)
}

func verifyEditorJWT(
	token, secret string,
	now time.Time,
) (map[string]any, error) {
	if len(token) == 0 || len(token) > 64<<10 {
		return nil, fmt.Errorf("office: missing or oversized engine token")
	}
	parts := splitToken(token)
	if len(parts) != 3 {
		return nil, fmt.Errorf("office: malformed engine token")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("office: malformed engine token")
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil ||
		header.Algorithm != "HS256" || header.Type != "JWT" {
		return nil, fmt.Errorf("office: unsupported engine token")
	}
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		return nil, fmt.Errorf("office: invalid engine token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) > 48<<10 {
		return nil, fmt.Errorf("office: malformed engine token")
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("office: malformed engine token")
	}
	expiry, ok := numericClaim(claims, "exp")
	if !ok || now.Unix() >= int64(expiry)+60 {
		return nil, fmt.Errorf("office: expired engine token")
	}
	return claims, nil
}

func signJWTBytes(payload []byte, secret string) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(signingInput))
	return signingInput + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func splitToken(token string) []string {
	parts := make([]string, 0, 3)
	start := 0
	for index := 0; index < len(token); index++ {
		if token[index] == '.' {
			parts = append(parts, token[start:index])
			start = index + 1
		}
	}
	parts = append(parts, token[start:])
	return parts
}

func numericClaim(claims map[string]any, name string) (float64, bool) {
	value, ok := claims[name]
	if !ok {
		return 0, false
	}
	number, ok := value.(float64)
	return number, ok
}
