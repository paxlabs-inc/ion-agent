package vault

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestDeriveUserKeyUsesHKDFSaltAndUserDomainSeparation(t *testing.T) {
	t.Parallel()
	master := repeatedKey(0x51)
	salt := repeatedKey(0x61)
	first, err := DeriveUserKey(master, "alice", salt)
	if err != nil {
		t.Fatal(err)
	}
	defer zero(first)
	again, err := DeriveUserKey(master, "alice", salt)
	if err != nil {
		t.Fatal(err)
	}
	defer zero(again)
	otherUser, err := DeriveUserKey(master, "bob", salt)
	if err != nil {
		t.Fatal(err)
	}
	defer zero(otherUser)
	otherSalt, err := DeriveUserKey(master, "alice", repeatedKey(0x62))
	if err != nil {
		t.Fatal(err)
	}
	defer zero(otherSalt)
	if !bytes.Equal(first, again) {
		t.Fatal("HKDF derivation is not deterministic")
	}
	if bytes.Equal(first, otherUser) || bytes.Equal(first, otherSalt) {
		t.Fatal("user or salt did not domain-separate derived key")
	}
}

func TestDeriveUserKeyAndUserInitializationValidation(t *testing.T) {
	t.Parallel()
	if _, err := DeriveUserKey([]byte("short"), "alice", repeatedKey(1)); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("short master error = %v", err)
	}
	if _, err := DeriveUserKey(repeatedKey(1), "", repeatedKey(2)); err == nil {
		t.Fatal("empty user succeeded")
	}
	if _, err := DeriveUserKey(repeatedKey(1), "alice", []byte("short")); err == nil {
		t.Fatal("short salt succeeded")
	}
	if _, err := initializeForUserWithReader(
		context.Background(),
		&memoryKEKSource{key: repeatedKey(1)},
		&memoryWrappedStore{loadErr: ErrKeyNotFound},
		"",
		bytes.NewReader(make([]byte, 128)),
	); err == nil {
		t.Fatal("empty initialization user succeeded")
	}
}
