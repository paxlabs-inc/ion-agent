package privatecomputer

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ArtifactExportReceipt struct {
	ArtifactID        uuid.UUID `json:"artifact_id"`
	InstallationID    uuid.UUID `json:"installation_id"`
	ActorID           uuid.UUID `json:"actor_id"`
	IonSessionID      uuid.UUID `json:"ion_session_id"`
	ComputerSessionID uuid.UUID `json:"computer_session_id"`
	AuthorityRevision uint64    `json:"authority_revision"`
	SHA256            string    `json:"sha256"`
	SizeBytes         int64     `json:"size_bytes"`
	VerifiedAt        time.Time `json:"verified_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	Signature         string    `json:"signature"`
}

type artifactExportClaims struct {
	ArtifactID        uuid.UUID `json:"artifact_id"`
	InstallationID    uuid.UUID `json:"installation_id"`
	ActorID           uuid.UUID `json:"actor_id"`
	IonSessionID      uuid.UUID `json:"ion_session_id"`
	ComputerSessionID uuid.UUID `json:"computer_session_id"`
	AuthorityRevision uint64    `json:"authority_revision"`
	SHA256            string    `json:"sha256"`
	SizeBytes         int64     `json:"size_bytes"`
	VerifiedAt        time.Time `json:"verified_at"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type SignedArtifactVerifier struct {
	publicKey ed25519.PublicKey
	clock     func() time.Time
}

func NewSignedArtifactVerifier(
	publicKey ed25519.PublicKey,
	clock func() time.Time,
) (*SignedArtifactVerifier, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidContract
	}
	if clock == nil {
		clock = time.Now
	}
	return &SignedArtifactVerifier{
		publicKey: append(ed25519.PublicKey(nil), publicKey...),
		clock:     clock,
	}, nil
}

func SignArtifactExportReceipt(
	privateKey ed25519.PrivateKey,
	receipt ArtifactExportReceipt,
) (ArtifactExportReceipt, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return ArtifactExportReceipt{}, ErrInvalidContract
	}
	payload, err := receipt.signingPayload()
	if err != nil {
		return ArtifactExportReceipt{}, err
	}
	receipt.Signature = base64.RawURLEncoding.EncodeToString(
		ed25519.Sign(privateKey, payload),
	)
	return receipt, nil
}

func (verifier *SignedArtifactVerifier) VerifyExport(
	_ context.Context,
	scope Scope,
	_ string,
	receipt ArtifactExportReceipt,
) error {
	now := verifier.clock().UTC()
	if receipt.ArtifactID == uuid.Nil ||
		receipt.InstallationID != scope.InstallationID ||
		receipt.ActorID != scope.ActorID ||
		receipt.IonSessionID != scope.IonSessionID ||
		receipt.ComputerSessionID != scope.ComputerSessionID ||
		receipt.AuthorityRevision == 0 ||
		receipt.SizeBytes < 0 ||
		receipt.VerifiedAt.IsZero() ||
		receipt.ExpiresAt.IsZero() ||
		receipt.VerifiedAt.After(now.Add(time.Minute)) ||
		receipt.VerifiedAt.Before(now.Add(-MaximumRequestTTL)) ||
		!receipt.ExpiresAt.After(now) ||
		receipt.ExpiresAt.After(receipt.VerifiedAt.Add(MaximumRequestTTL)) ||
		!validSHA256(receipt.SHA256) {
		return ErrArtifactRequired
	}
	signature, err := base64.RawURLEncoding.DecodeString(receipt.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return ErrArtifactRequired
	}
	payload, err := receipt.signingPayload()
	if err != nil ||
		!ed25519.Verify(verifier.publicKey, payload, signature) {
		return ErrArtifactRequired
	}
	return nil
}

func (receipt ArtifactExportReceipt) signingPayload() ([]byte, error) {
	claims := artifactExportClaims{
		ArtifactID:        receipt.ArtifactID,
		InstallationID:    receipt.InstallationID,
		ActorID:           receipt.ActorID,
		IonSessionID:      receipt.IonSessionID,
		ComputerSessionID: receipt.ComputerSessionID,
		AuthorityRevision: receipt.AuthorityRevision,
		SHA256:            strings.ToLower(receipt.SHA256),
		SizeBytes:         receipt.SizeBytes,
		VerifiedAt:        receipt.VerifiedAt.UTC(),
		ExpiresAt:         receipt.ExpiresAt.UTC(),
	}
	payload, err := json.Marshal(claims)
	if err != nil || len(payload) > MaximumRequestBytes {
		return nil, ErrInvalidContract
	}
	return payload, nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
