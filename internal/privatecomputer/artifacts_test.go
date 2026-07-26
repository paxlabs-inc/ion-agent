package privatecomputer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSignedArtifactReceiptBindsAuthoritativeScopeAndExpiry(t *testing.T) {
	now := time.Date(2026, 7, 24, 17, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := NewSignedArtifactVerifier(
		publicKey,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope(ModeClean)
	receipt := ArtifactExportReceipt{
		ArtifactID:        uuid.New(),
		InstallationID:    scope.InstallationID,
		ActorID:           scope.ActorID,
		IonSessionID:      scope.IonSessionID,
		ComputerSessionID: scope.ComputerSessionID,
		AuthorityRevision: 4,
		SHA256:            hex.EncodeToString([]byte(strings.Repeat("a", 32))),
		SizeBytes:         4096,
		VerifiedAt:        now,
		ExpiresAt:         now.Add(4 * time.Minute),
	}
	receipt, err = SignArtifactExportReceipt(privateKey, receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifier.VerifyExport(
		context.Background(),
		scope,
		t.TempDir(),
		receipt,
	); err != nil {
		t.Fatal(err)
	}

	crossActor := scope
	crossActor.ActorID = uuid.New()
	if err := verifier.VerifyExport(
		context.Background(),
		crossActor,
		t.TempDir(),
		receipt,
	); !errors.Is(err, ErrArtifactRequired) {
		t.Fatalf("cross-actor receipt = %v", err)
	}
	tampered := receipt
	tampered.SizeBytes++
	if err := verifier.VerifyExport(
		context.Background(),
		scope,
		t.TempDir(),
		tampered,
	); !errors.Is(err, ErrArtifactRequired) {
		t.Fatalf("tampered receipt = %v", err)
	}
	now = now.Add(5 * time.Minute)
	if err := verifier.VerifyExport(
		context.Background(),
		scope,
		t.TempDir(),
		receipt,
	); !errors.Is(err, ErrArtifactRequired) {
		t.Fatalf("expired receipt = %v", err)
	}
}
