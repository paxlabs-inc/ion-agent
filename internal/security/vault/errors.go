// Package vault implements the Ion envelope-encryption hierarchy.
package vault

import "errors"

var (
	// ErrDecryptionFailed deliberately hides which envelope component failed.
	ErrDecryptionFailed = errors.New("vault: decryption failed")
	// ErrClosed is returned after key material has been zeroed.
	ErrClosed = errors.New("vault: closed")
	// ErrInvalidKey is returned when a key does not contain 256 bits.
	ErrInvalidKey = errors.New("vault: AES-256 keys must be 32 bytes")
	// ErrKeyNotFound indicates that a key source has not been initialized.
	ErrKeyNotFound = errors.New("vault: key not found")
)
