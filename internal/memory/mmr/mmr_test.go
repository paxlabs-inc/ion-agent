package mmr

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendRootAndProofs(t *testing.T) {
	t.Parallel()
	rangeValue := New()
	var leaves []Hash
	for index := 0; index < 13; index++ {
		leaf := Sum([]byte{byte(index)})
		leaves = append(leaves, leaf)
		gotIndex, _, err := rangeValue.AppendHash(leaf)
		if err != nil || gotIndex != uint64(index) {
			t.Fatalf("AppendHash() = %d, %v", gotIndex, err)
		}
	}
	root := rangeValue.Root()
	for index, leaf := range leaves {
		proof, err := rangeValue.Prove(uint64(index))
		if err != nil {
			t.Fatalf("Prove(%d) error = %v", index, err)
		}
		if !VerifyProof(leaf, proof, root) {
			t.Fatalf("proof %d did not verify", index)
		}
		bad := leaf
		bad[0] ^= 1
		if VerifyProof(bad, proof, root) {
			t.Fatalf("fabricated leaf %d verified", index)
		}
	}
}

func TestPersistentFormatRoundTripAndCorruption(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mmr.dat")
	rangeValue, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	for index := 0; index < 5; index++ {
		if _, _, err := rangeValue.AppendHash(Sum([]byte{byte(index)})); err != nil {
			t.Fatalf("AppendHash() error = %v", err)
		}
	}
	root := rangeValue.Root()
	if err := rangeValue.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("Open(reopen) error = %v", err)
	}
	if reopened.LeafCount() != 5 || reopened.Root() != root {
		t.Fatalf("reopened count/root mismatch")
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if _, err := Open(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open(corrupt) error = %v", err)
	}
}
