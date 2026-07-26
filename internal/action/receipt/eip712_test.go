package receipt

import (
	"bytes"
	"errors"
	"testing"
	"time"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

func TestEIP712ReceiptRoundTripAndTamperDetection(t *testing.T) {
	t.Parallel()
	domain := testDomain()
	signer, err := NewSigner(bytes.Repeat([]byte{0x42}, 32), domain)
	if err != nil {
		t.Fatal(err)
	}
	operation := Operation{
		ID:          "deploy:production:release-42",
		PayloadHash: keccak256([]byte("immutable deployment manifest")),
		Sequence:    7,
		Timestamp:   time.Unix(1_750_000_000, 0).UTC(),
	}
	first, err := signer.Sign(operation)
	if err != nil {
		t.Fatal(err)
	}
	second, err := signer.Sign(operation)
	if err != nil {
		t.Fatal(err)
	}
	if first.Signature != second.Signature {
		t.Fatal("RFC6979 signature was not deterministic")
	}
	if first.Signer != signer.Address() {
		t.Fatal("receipt signer address mismatch")
	}
	if err := Verify(first); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	tampered := first
	tampered.Operation.PayloadHash[0] ^= 1
	if err := Verify(tampered); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("tampered payload error = %v", err)
	}
	tampered = first
	tampered.Signature[10] ^= 1
	if err := Verify(tampered); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("tampered signature error = %v", err)
	}
	tampered = first
	tampered.Domain.ChainID++
	if err := Verify(tampered); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("cross-domain replay error = %v", err)
	}
}

func TestEIP712ValidationAndEntropyFailure(t *testing.T) {
	t.Parallel()
	if _, err := NewSigner(nil, testDomain()); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("short key error = %v", err)
	}
	if _, err := NewSigner(make([]byte, 32), testDomain()); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("zero key error = %v", err)
	}
	overflow := bytes.Repeat([]byte{0xff}, 32)
	if _, err := NewSigner(overflow, testDomain()); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("overflow key error = %v", err)
	}
	if _, err := GenerateFrom(&failingReader{}, testDomain()); err == nil {
		t.Fatal("entropy failure succeeded")
	}
	if _, err := Generate(Domain{}); !errors.Is(err, ErrInvalidDomain) {
		t.Fatalf("invalid domain error = %v", err)
	}
	signer, err := Generate(testDomain())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signer.Sign(Operation{}); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("invalid operation error = %v", err)
	}
}

func TestEIP712SignatureIsRecoverableBySecp256k1(t *testing.T) {
	t.Parallel()
	key, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner(key.Serialize(), testDomain())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := signer.Sign(Operation{
		ID:        "pay:invoice:123",
		Sequence:  1,
		Timestamp: time.Unix(1, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(receipt); err != nil {
		t.Fatal(err)
	}
}

func testDomain() Domain {
	return Domain{
		Name:    "Ion",
		Version: "1",
		ChainID: 1,
		Salt:    keccak256([]byte("test installation")),
	}
}

type failingReader struct{}

func (*failingReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}
