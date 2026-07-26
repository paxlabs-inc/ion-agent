package smt

import "testing"

func TestMembershipUpdateDeleteAndNonMembership(t *testing.T) {
	t.Parallel()
	tree := New()
	key := Key([]byte("memory-1"))
	other := Key([]byte("memory-2"))
	emptyRoot := tree.Root()
	nonMember := tree.Prove(key)
	if !VerifyNonMembership(key, nonMember, emptyRoot) {
		t.Fatal("empty non-membership proof failed")
	}
	root := tree.Update(key, []byte("version-1"))
	member := tree.Prove(key)
	if !VerifyMembership(key, []byte("version-1"), member, root) {
		t.Fatal("membership proof failed")
	}
	if VerifyMembership(key, []byte("fabricated"), member, root) {
		t.Fatal("fabricated value verified")
	}
	if !VerifyNonMembership(other, tree.Prove(other), root) {
		t.Fatal("non-membership proof failed")
	}
	updated := tree.Update(key, []byte("version-2"))
	if updated == root {
		t.Fatal("update did not change root")
	}
	if tree.Delete(key) != emptyRoot {
		t.Fatal("delete did not restore empty root")
	}
}

func TestForestRootIsNamespaceOrderIndependent(t *testing.T) {
	t.Parallel()
	first := NewForest()
	second := NewForest()
	keyA := Key([]byte("a"))
	keyB := Key([]byte("b"))
	_, _ = first.Update("0x01", keyA, []byte("a"))
	_, _ = first.Update("0x02", keyB, []byte("b"))
	_, _ = second.Update("0x02", keyB, []byte("b"))
	_, _ = second.Update("0x01", keyA, []byte("a"))
	if first.Root() != second.Root() {
		t.Fatal("forest root depends on namespace insertion order")
	}
}
