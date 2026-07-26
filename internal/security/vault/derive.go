package vault

import (
	"crypto/sha256"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const UserSaltSize = 32

// DeriveUserKey derives a per-user key with HKDF-SHA256. The user identity is
// domain-separated in the info field and salt must be independently generated
// for each user initialization.
func DeriveUserKey(master []byte, userID string, salt []byte) ([]byte, error) {
	if len(master) != KeySize {
		return nil, ErrInvalidKey
	}
	userID = strings.TrimSpace(userID)
	if userID == "" || len(salt) < UserSaltSize {
		return nil, fmt.Errorf("vault: user identity and %d-byte salt are required", UserSaltSize)
	}
	reader := hkdf.New(
		sha256.New,
		master,
		salt,
		[]byte("ion/user-key/v1\x00"+userID),
	)
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(reader, key); err != nil {
		zero(key)
		return nil, fmt.Errorf("vault: derive User Key: %w", err)
	}
	return key, nil
}
