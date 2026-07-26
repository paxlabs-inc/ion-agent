// Package smt implements 256-bit Sparse Merkle Trees and namespace forests.
package smt

import (
	"encoding/binary"
	"fmt"
	"sort"
	"sync"

	"lukechampine.com/blake3"
)

// Hash is a BLAKE3-256 digest.
type Hash [32]byte

var emptyHashes = buildEmptyHashes()

// Proof is a fixed-depth membership or non-membership proof. Siblings are
// ordered from the leaf level toward the root.
type Proof struct {
	Exists    bool
	ValueHash Hash
	Siblings  []Hash
}

// Tree commits key/value hashes without materializing the 2^256 empty leaves.
type Tree struct {
	mu     sync.RWMutex
	values map[Hash]Hash
	root   Hash
}

// New constructs an empty tree.
func New() *Tree {
	return &Tree{
		values: make(map[Hash]Hash),
		root:   emptyHashes[0],
	}
}

// Key hashes an arbitrary identifier into a 256-bit SMT path.
func Key(identifier []byte) Hash {
	return sum(identifier)
}

// Update inserts or replaces a key. A nil value deletes it.
func (tree *Tree) Update(key Hash, value []byte) Hash {
	if value == nil {
		return tree.Delete(key)
	}
	return tree.UpdateHash(key, sum(value))
}

// UpdateHash inserts a caller-computed value commitment.
func (tree *Tree) UpdateHash(key, valueHash Hash) Hash {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	tree.values[key] = valueHash
	tree.root = rootFor(tree.values)
	return tree.root
}

// Delete removes a key while preserving a non-membership proof.
func (tree *Tree) Delete(key Hash) Hash {
	tree.mu.Lock()
	defer tree.mu.Unlock()
	delete(tree.values, key)
	tree.root = rootFor(tree.values)
	return tree.root
}

// Root returns the current namespace commitment.
func (tree *Tree) Root() Hash {
	tree.mu.RLock()
	defer tree.mu.RUnlock()
	return tree.root
}

// Get returns a committed value hash.
func (tree *Tree) Get(key Hash) (Hash, bool) {
	tree.mu.RLock()
	defer tree.mu.RUnlock()
	value, exists := tree.values[key]
	return value, exists
}

// Prove returns either a membership or non-membership proof.
func (tree *Tree) Prove(key Hash) Proof {
	tree.mu.RLock()
	defer tree.mu.RUnlock()
	levels := buildLevels(tree.values)
	valueHash, exists := tree.values[key]
	proof := Proof{
		Exists:    exists,
		ValueHash: valueHash,
		Siblings:  make([]Hash, 0, 256),
	}
	for childDepth := 256; childDepth > 0; childDepth-- {
		sibling := prefixFor(key, childDepth)
		toggleBit(&sibling, childDepth-1)
		hash, found := levels[childDepth][sibling]
		if !found {
			hash = emptyHashes[childDepth]
		}
		proof.Siblings = append(proof.Siblings, hash)
	}
	return proof
}

// VerifyMembership checks a value against a namespace root.
func VerifyMembership(key Hash, value []byte, proof Proof, root Hash) bool {
	if !proof.Exists || proof.ValueHash != sum(value) {
		return false
	}
	return verify(key, proof, root)
}

// VerifyMembershipHash checks an already-hashed value.
func VerifyMembershipHash(key, valueHash Hash, proof Proof, root Hash) bool {
	if !proof.Exists || proof.ValueHash != valueHash {
		return false
	}
	return verify(key, proof, root)
}

// VerifyNonMembership proves that the key resolves to the canonical empty leaf.
func VerifyNonMembership(key Hash, proof Proof, root Hash) bool {
	return !proof.Exists && proof.ValueHash == (Hash{}) && verify(key, proof, root)
}

func verify(key Hash, proof Proof, root Hash) bool {
	if len(proof.Siblings) != 256 {
		return false
	}
	current := emptyHashes[256]
	if proof.Exists {
		current = leafHash(key, proof.ValueHash)
	}
	for childDepth := 256; childDepth > 0; childDepth-- {
		sibling := proof.Siblings[256-childDepth]
		if bit(key, childDepth-1) == 0 {
			current = branchHash(current, sibling)
		} else {
			current = branchHash(sibling, current)
		}
	}
	return current == root
}

// Forest owns one independent SMT per namespace and computes a deterministic
// aggregate root for binding into the MMR.
type Forest struct {
	mu    sync.RWMutex
	trees map[string]*Tree
}

// NewForest constructs an empty namespace collection.
func NewForest() *Forest {
	return &Forest{trees: make(map[string]*Tree)}
}

// Update applies a value to one namespace.
func (forest *Forest) Update(namespace string, key Hash, value []byte) (Hash, error) {
	if namespace == "" {
		return Hash{}, fmt.Errorf("smt: namespace is required")
	}
	forest.mu.Lock()
	tree := forest.trees[namespace]
	if tree == nil {
		tree = New()
		forest.trees[namespace] = tree
	}
	forest.mu.Unlock()
	return tree.Update(key, value), nil
}

// Delete removes a value from one namespace.
func (forest *Forest) Delete(namespace string, key Hash) (Hash, error) {
	if namespace == "" {
		return Hash{}, fmt.Errorf("smt: namespace is required")
	}
	forest.mu.RLock()
	tree := forest.trees[namespace]
	forest.mu.RUnlock()
	if tree == nil {
		return emptyHashes[0], nil
	}
	return tree.Delete(key), nil
}

// Tree returns a namespace tree when it exists.
func (forest *Forest) Tree(namespace string) (*Tree, bool) {
	forest.mu.RLock()
	defer forest.mu.RUnlock()
	tree, exists := forest.trees[namespace]
	return tree, exists
}

// Roots returns a defensive, deterministic snapshot by namespace.
func (forest *Forest) Roots() map[string]Hash {
	forest.mu.RLock()
	defer forest.mu.RUnlock()
	roots := make(map[string]Hash, len(forest.trees))
	for namespace, tree := range forest.trees {
		roots[namespace] = tree.Root()
	}
	return roots
}

// Root binds sorted namespace names to their independent roots.
func (forest *Forest) Root() Hash {
	roots := forest.Roots()
	names := make([]string, 0, len(roots))
	for namespace := range roots {
		names = append(names, namespace)
	}
	sort.Strings(names)
	hasher := blake3.New(32, nil)
	_, _ = hasher.Write([]byte("PROM-SMT-FOREST\x00"))
	for _, namespace := range names {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(namespace)))
		_, _ = hasher.Write(length[:])
		_, _ = hasher.Write([]byte(namespace))
		root := roots[namespace]
		_, _ = hasher.Write(root[:])
	}
	var result Hash
	copy(result[:], hasher.Sum(nil))
	return result
}

type levelMap map[Hash]Hash

func rootFor(values map[Hash]Hash) Hash {
	levels := buildLevels(values)
	var zero Hash
	if root, exists := levels[0][zero]; exists {
		return root
	}
	return emptyHashes[0]
}

func buildLevels(values map[Hash]Hash) [257]levelMap {
	var levels [257]levelMap
	leaves := make(levelMap, len(values))
	for key, valueHash := range values {
		leaves[key] = leafHash(key, valueHash)
	}
	levels[256] = leaves
	current := leaves
	for depth := 255; depth >= 0; depth-- {
		parents := make(map[Hash]struct{}, len(current))
		for child := range current {
			parents[prefixFor(child, depth)] = struct{}{}
		}
		next := make(levelMap, len(parents))
		for parent := range parents {
			leftPrefix := prefixFor(parent, depth+1)
			rightPrefix := leftPrefix
			toggleBit(&rightPrefix, depth)
			left, exists := current[leftPrefix]
			if !exists {
				left = emptyHashes[depth+1]
			}
			right, exists := current[rightPrefix]
			if !exists {
				right = emptyHashes[depth+1]
			}
			next[parent] = branchHash(left, right)
		}
		levels[depth] = next
		current = next
	}
	return levels
}

func prefixFor(key Hash, depth int) Hash {
	if depth >= 256 {
		return key
	}
	var prefix Hash
	fullBytes := depth / 8
	copy(prefix[:fullBytes], key[:fullBytes])
	remaining := depth % 8
	if remaining > 0 {
		prefix[fullBytes] = key[fullBytes] & byte(0xff<<(8-remaining))
	}
	return prefix
}

func bit(key Hash, position int) byte {
	return (key[position/8] >> (7 - (position % 8))) & 1
}

func toggleBit(key *Hash, position int) {
	key[position/8] ^= byte(1 << (7 - (position % 8)))
}

func leafHash(key, valueHash Hash) Hash {
	var input [65]byte
	input[0] = 0
	copy(input[1:33], key[:])
	copy(input[33:], valueHash[:])
	return sum(input[:])
}

func branchHash(left, right Hash) Hash {
	var input [65]byte
	input[0] = 1
	copy(input[1:33], left[:])
	copy(input[33:], right[:])
	return sum(input[:])
}

func buildEmptyHashes() [257]Hash {
	var hashes [257]Hash
	hashes[256] = sum([]byte{2})
	for depth := 255; depth >= 0; depth-- {
		hashes[depth] = branchHash(hashes[depth+1], hashes[depth+1])
	}
	return hashes
}

func sum(content []byte) Hash {
	return Hash(blake3.Sum256(content))
}
