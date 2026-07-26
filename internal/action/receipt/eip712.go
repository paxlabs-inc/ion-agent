// Package receipt produces EIP-712-compatible secp256k1 operation receipts.
package receipt

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

const SignatureSize = 65

var (
	ErrInvalidDomain    = errors.New("receipt: invalid EIP-712 domain")
	ErrInvalidKey       = errors.New("receipt: invalid secp256k1 private key")
	ErrInvalidReceipt   = errors.New("receipt: invalid receipt")
	ErrSignatureInvalid = errors.New("receipt: signature verification failed")
)

var (
	domainTypeHash = keccak256([]byte(
		"EIP712Domain(string name,string version,uint256 chainId,bytes32 salt)",
	))
	receiptTypeHash = keccak256([]byte(
		"OperationReceipt(bytes32 operationId,bytes32 payloadHash,uint64 sequence,int64 timestamp)",
	))
)

// Domain separates receipts by product, deployment, chain, and installation.
type Domain struct {
	Name    string
	Version string
	ChainID uint64
	Salt    [32]byte
}

// Operation is the canonical consequential-action statement being signed.
type Operation struct {
	ID          string
	PayloadHash [32]byte
	Sequence    uint64
	Timestamp   time.Time
}

// Receipt contains enough data to independently reproduce and verify the
// EIP-712 digest. Signature is Ethereum r || s || v form with v in {27, 28}.
type Receipt struct {
	Domain    Domain
	Operation Operation
	Digest    [32]byte
	Signer    [20]byte
	Signature [SignatureSize]byte
}

// Signer owns a secp256k1 key and an immutable EIP-712 domain.
type Signer struct {
	private *secp256k1.PrivateKey
	domain  Domain
	address [20]byte
}

// Generate creates a signer using a CSPRNG.
func Generate(domain Domain) (*Signer, error) {
	return GenerateFrom(rand.Reader, domain)
}

// GenerateFrom exists to make entropy failure paths testable.
func GenerateFrom(random io.Reader, domain Domain) (*Signer, error) {
	if random == nil {
		return nil, fmt.Errorf("receipt: random source is required")
	}
	if err := validateDomain(domain); err != nil {
		return nil, err
	}
	key, err := secp256k1.GeneratePrivateKeyFromRand(random)
	if err != nil {
		return nil, fmt.Errorf("receipt: generate secp256k1 key: %w", err)
	}
	return newSigner(key, domain), nil
}

// NewSigner imports an existing 32-byte secp256k1 private key.
func NewSigner(privateKey []byte, domain Domain) (*Signer, error) {
	if err := validateDomain(domain); err != nil {
		return nil, err
	}
	if len(privateKey) != secp256k1.PrivKeyBytesLen {
		return nil, ErrInvalidKey
	}
	var scalar secp256k1.ModNScalar
	if scalar.SetByteSlice(privateKey) || scalar.IsZero() {
		scalar.Zero()
		return nil, ErrInvalidKey
	}
	key := secp256k1.NewPrivateKey(&scalar)
	scalar.Zero()
	return newSigner(key, domain), nil
}

func newSigner(private *secp256k1.PrivateKey, domain Domain) *Signer {
	return &Signer{
		private: private,
		domain:  domain,
		address: address(private.PubKey()),
	}
}

// Address is the Ethereum-compatible address of the signing key.
func (signer *Signer) Address() [20]byte {
	return signer.address
}

// Sign returns a deterministic RFC6979 secp256k1 signature over the EIP-712
// typed-data digest.
func (signer *Signer) Sign(operation Operation) (Receipt, error) {
	if signer == nil || signer.private == nil {
		return Receipt{}, ErrInvalidKey
	}
	digest, err := Digest(signer.domain, operation)
	if err != nil {
		return Receipt{}, err
	}
	compact := ecdsa.SignCompact(signer.private, digest[:], false)
	if len(compact) != SignatureSize || compact[0] < 27 || compact[0] > 28 {
		return Receipt{}, ErrSignatureInvalid
	}
	var signature [SignatureSize]byte
	copy(signature[:64], compact[1:])
	signature[64] = compact[0]
	return Receipt{
		Domain:    signer.domain,
		Operation: operation,
		Digest:    digest,
		Signer:    signer.address,
		Signature: signature,
	}, nil
}

// Verify checks the typed-data digest, recovers the signer, and compares the
// recovered address with the receipt's committed address.
func Verify(candidate Receipt) error {
	digest, err := Digest(candidate.Domain, candidate.Operation)
	if err != nil {
		return err
	}
	if !bytes.Equal(digest[:], candidate.Digest[:]) {
		return fmt.Errorf("%w: digest mismatch", ErrInvalidReceipt)
	}
	v := candidate.Signature[64]
	if v != 27 && v != 28 {
		return fmt.Errorf("%w: invalid recovery id", ErrInvalidReceipt)
	}
	compact := make([]byte, SignatureSize)
	compact[0] = v
	copy(compact[1:], candidate.Signature[:64])
	public, _, err := ecdsa.RecoverCompact(compact, digest[:])
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
	}
	if recovered := address(public); !bytes.Equal(recovered[:], candidate.Signer[:]) {
		return ErrSignatureInvalid
	}
	return nil
}

// PayloadHash returns the Keccak-256 commitment used in operation receipts.
func PayloadHash(canonicalPayload []byte) [32]byte {
	return keccak256(canonicalPayload)
}

// Digest computes keccak256("\x19\x01" || domainSeparator || structHash).
func Digest(domain Domain, operation Operation) ([32]byte, error) {
	if err := validateDomain(domain); err != nil {
		return [32]byte{}, err
	}
	if operation.ID == "" || operation.Timestamp.IsZero() ||
		operation.Timestamp.Unix() < 0 {
		return [32]byte{}, fmt.Errorf("%w: incomplete operation", ErrInvalidReceipt)
	}
	domainHash := hashDomain(domain)
	operationHash := hashOperation(operation)
	payload := make([]byte, 0, 66)
	payload = append(payload, 0x19, 0x01)
	payload = append(payload, domainHash[:]...)
	payload = append(payload, operationHash[:]...)
	return keccak256(payload), nil
}

func validateDomain(domain Domain) error {
	if domain.Name == "" || domain.Version == "" || domain.ChainID == 0 {
		return ErrInvalidDomain
	}
	return nil
}

func hashDomain(domain Domain) [32]byte {
	encoded := make([]byte, 0, 32*5)
	encoded = append(encoded, domainTypeHash[:]...)
	nameHash := keccak256([]byte(domain.Name))
	versionHash := keccak256([]byte(domain.Version))
	encoded = append(encoded, nameHash[:]...)
	encoded = append(encoded, versionHash[:]...)
	encoded = append(encoded, uint256(domain.ChainID)...)
	encoded = append(encoded, domain.Salt[:]...)
	return keccak256(encoded)
}

func hashOperation(operation Operation) [32]byte {
	encoded := make([]byte, 0, 32*5)
	encoded = append(encoded, receiptTypeHash[:]...)
	idHash := keccak256([]byte(operation.ID))
	encoded = append(encoded, idHash[:]...)
	encoded = append(encoded, operation.PayloadHash[:]...)
	encoded = append(encoded, uint256(operation.Sequence)...)
	encoded = append(encoded, int256(operation.Timestamp.Unix())...)
	return keccak256(encoded)
}

func uint256(value uint64) []byte {
	encoded := make([]byte, 32)
	binary.BigEndian.PutUint64(encoded[24:], value)
	return encoded
}

func int256(value int64) []byte {
	encoded := make([]byte, 32)
	binary.BigEndian.PutUint64(encoded[24:], uint64(value))
	return encoded
}

func address(public *secp256k1.PublicKey) [20]byte {
	serialized := public.SerializeUncompressed()
	hash := keccak256(serialized[1:])
	var result [20]byte
	copy(result[:], hash[12:])
	return result
}

func keccak256(data []byte) [32]byte {
	hasher := sha3.NewLegacyKeccak256()
	_, _ = hasher.Write(data)
	var result [32]byte
	copy(result[:], hasher.Sum(nil))
	return result
}
