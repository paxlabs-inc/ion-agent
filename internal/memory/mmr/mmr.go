// Package mmr implements an append-only BLAKE3 Merkle Mountain Range.
package mmr

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"lukechampine.com/blake3"
)

const (
	fileVersion = uint32(1)
	headerSize  = 8 + 4 + 8
)

var (
	fileMagic = [8]byte{'P', 'R', 'O', 'M', 'M', 'M', 'R', 0}
	// ErrInvalidProof indicates an internally inconsistent inclusion proof.
	ErrInvalidProof = errors.New("mmr: invalid proof")
	// ErrCorrupt indicates malformed or inconsistent persistent MMR bytes.
	ErrCorrupt = errors.New("mmr: corrupt data")
)

// Hash is a BLAKE3-256 digest.
type Hash [32]byte

// Proof proves one leaf's inclusion in a particular leaf-count snapshot.
type Proof struct {
	LeafIndex uint64
	LeafCount uint64
	PeakIndex int
	Siblings  []Hash
	Peaks     []Hash
}

// MMR stores leaves because the specified durable format is a leaf-hash log.
// Peaks and proofs are deterministic derived state.
type MMR struct {
	mu     sync.RWMutex
	leaves []Hash
	file   *os.File
}

// New constructs an in-memory range.
func New() *MMR {
	return &MMR{}
}

// Open reads or initializes the standard PROMMMR file.
func Open(path string) (*MMR, error) {
	if path == "" {
		return nil, fmt.Errorf("mmr: path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("mmr: create directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("mmr: open: %w", err)
	}
	closeWith := func(err error) (*MMR, error) {
		_ = file.Close()
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return closeWith(fmt.Errorf("mmr: stat: %w", err))
	}
	if info.Size() == 0 {
		header := make([]byte, headerSize)
		copy(header[:8], fileMagic[:])
		binary.BigEndian.PutUint32(header[8:12], fileVersion)
		if _, err := file.Write(header); err != nil {
			return closeWith(fmt.Errorf("mmr: initialize: %w", err))
		}
		if err := file.Sync(); err != nil {
			return closeWith(fmt.Errorf("mmr: sync header: %w", err))
		}
		return &MMR{file: file}, nil
	}
	if info.Size() < headerSize {
		return closeWith(ErrCorrupt)
	}
	header := make([]byte, headerSize)
	if _, err := file.ReadAt(header, 0); err != nil {
		return closeWith(fmt.Errorf("%w: read header: %v", ErrCorrupt, err))
	}
	if string(header[:8]) != string(fileMagic[:]) ||
		binary.BigEndian.Uint32(header[8:12]) != fileVersion {
		return closeWith(ErrCorrupt)
	}
	count := binary.BigEndian.Uint64(header[12:20])
	expectedSize := int64(headerSize) + int64(count)*32
	if info.Size() != expectedSize {
		return closeWith(fmt.Errorf("%w: leaf count does not match file size", ErrCorrupt))
	}
	leaves := make([]Hash, count)
	for index := range leaves {
		offset := int64(headerSize) + int64(index)*32
		if _, err := file.ReadAt(leaves[index][:], offset); err != nil {
			return closeWith(fmt.Errorf("%w: read leaf: %v", ErrCorrupt, err))
		}
	}
	return &MMR{leaves: leaves, file: file}, nil
}

// LeafHash implements BLAKE3(journal_entry_bytes || SMT_root_at_seq).
func LeafHash(journalEntry []byte, smtRoot Hash) Hash {
	hasher := blake3.New(32, nil)
	_, _ = hasher.Write(journalEntry)
	_, _ = hasher.Write(smtRoot[:])
	var result Hash
	_, _ = io.ReadFull(hasher.XOF(), result[:])
	return result
}

// Sum computes BLAKE3-256 over one byte string.
func Sum(content []byte) Hash {
	return Hash(blake3.Sum256(content))
}

// AppendEntry hashes and appends one journal entry plus its state commitment.
func (rangeValue *MMR) AppendEntry(journalEntry []byte, smtRoot Hash) (uint64, Hash, error) {
	return rangeValue.AppendHash(LeafHash(journalEntry, smtRoot))
}

// AppendHash appends a precomputed leaf and returns its index and new root.
func (rangeValue *MMR) AppendHash(leaf Hash) (uint64, Hash, error) {
	rangeValue.mu.Lock()
	defer rangeValue.mu.Unlock()
	index := uint64(len(rangeValue.leaves))
	if rangeValue.file != nil {
		offset := int64(headerSize) + int64(index)*32
		if _, err := rangeValue.file.WriteAt(leaf[:], offset); err != nil {
			return 0, Hash{}, fmt.Errorf("mmr: append leaf: %w", err)
		}
		if err := rangeValue.file.Sync(); err != nil {
			return 0, Hash{}, fmt.Errorf("mmr: sync leaf: %w", err)
		}
		var count [8]byte
		binary.BigEndian.PutUint64(count[:], index+1)
		if _, err := rangeValue.file.WriteAt(count[:], 12); err != nil {
			return 0, Hash{}, fmt.Errorf("mmr: update leaf count: %w", err)
		}
		if err := rangeValue.file.Sync(); err != nil {
			return 0, Hash{}, fmt.Errorf("mmr: sync leaf count: %w", err)
		}
	}
	rangeValue.leaves = append(rangeValue.leaves, leaf)
	return index, rootFor(rangeValue.leaves), nil
}

// Root returns the current bagged-peaks root.
func (rangeValue *MMR) Root() Hash {
	rangeValue.mu.RLock()
	defer rangeValue.mu.RUnlock()
	return rootFor(rangeValue.leaves)
}

// RootAt returns the bagged-peaks root for an historical leaf-count snapshot.
func (rangeValue *MMR) RootAt(leafCount uint64) (Hash, error) {
	rangeValue.mu.RLock()
	defer rangeValue.mu.RUnlock()
	if leafCount > uint64(len(rangeValue.leaves)) {
		return Hash{}, fmt.Errorf("%w: leaf count out of range", ErrInvalidProof)
	}
	return rootFor(rangeValue.leaves[:leafCount]), nil
}

// LeafCount returns the number of committed leaves.
func (rangeValue *MMR) LeafCount() uint64 {
	rangeValue.mu.RLock()
	defer rangeValue.mu.RUnlock()
	return uint64(len(rangeValue.leaves))
}

// Leaf returns a committed leaf hash.
func (rangeValue *MMR) Leaf(index uint64) (Hash, bool) {
	rangeValue.mu.RLock()
	defer rangeValue.mu.RUnlock()
	if index >= uint64(len(rangeValue.leaves)) {
		return Hash{}, false
	}
	return rangeValue.leaves[index], true
}

// Prove builds a logarithmic path inside the leaf's mountain plus all peaks.
func (rangeValue *MMR) Prove(index uint64) (Proof, error) {
	rangeValue.mu.RLock()
	defer rangeValue.mu.RUnlock()
	return prove(rangeValue.leaves, index)
}

// ProveAt builds an inclusion proof at an historical leaf-count snapshot.
func (rangeValue *MMR) ProveAt(index, leafCount uint64) (Proof, error) {
	rangeValue.mu.RLock()
	defer rangeValue.mu.RUnlock()
	if leafCount > uint64(len(rangeValue.leaves)) {
		return Proof{}, fmt.Errorf("%w: leaf count out of range", ErrInvalidProof)
	}
	return prove(rangeValue.leaves[:leafCount], index)
}

func prove(leaves []Hash, index uint64) (Proof, error) {
	if index >= uint64(len(leaves)) {
		return Proof{}, fmt.Errorf("%w: leaf index out of range", ErrInvalidProof)
	}
	ranges := peakRanges(uint64(len(leaves)))
	proof := Proof{
		LeafIndex: index,
		LeafCount: uint64(len(leaves)),
		PeakIndex: -1,
		Peaks:     make([]Hash, len(ranges)),
	}
	var mountain peakRange
	for peakIndex, candidate := range ranges {
		proof.Peaks[peakIndex] = perfectRoot(leaves, candidate.start, candidate.size)
		if index >= candidate.start && index < candidate.start+candidate.size {
			proof.PeakIndex = peakIndex
			mountain = candidate
		}
	}
	if proof.PeakIndex < 0 {
		return Proof{}, ErrInvalidProof
	}
	local := index - mountain.start
	for width := uint64(1); width < mountain.size; width <<= 1 {
		siblingBlock := ((local / width) ^ 1) * width
		proof.Siblings = append(
			proof.Siblings,
			perfectRoot(leaves, mountain.start+siblingBlock, width),
		)
	}
	return proof, nil
}

// VerifyProof verifies a proof against an expected snapshot root.
func VerifyProof(leaf Hash, proof Proof, expectedRoot Hash) bool {
	if proof.LeafCount == 0 || proof.LeafIndex >= proof.LeafCount {
		return false
	}
	ranges := peakRanges(proof.LeafCount)
	if proof.PeakIndex < 0 || proof.PeakIndex >= len(ranges) ||
		len(proof.Peaks) != len(ranges) {
		return false
	}
	mountain := ranges[proof.PeakIndex]
	if proof.LeafIndex < mountain.start ||
		proof.LeafIndex >= mountain.start+mountain.size {
		return false
	}
	expectedSiblings := 0
	for size := mountain.size; size > 1; size >>= 1 {
		expectedSiblings++
	}
	if len(proof.Siblings) != expectedSiblings {
		return false
	}
	current := leaf
	local := proof.LeafIndex - mountain.start
	for level, sibling := range proof.Siblings {
		if local&(uint64(1)<<level) == 0 {
			current = parentHash(current, sibling)
		} else {
			current = parentHash(sibling, current)
		}
	}
	peaks := append([]Hash(nil), proof.Peaks...)
	peaks[proof.PeakIndex] = current
	return bagPeaks(peaks) == expectedRoot
}

// Close closes persistent storage. In-memory ranges need not be closed.
func (rangeValue *MMR) Close() error {
	rangeValue.mu.Lock()
	defer rangeValue.mu.Unlock()
	if rangeValue.file == nil {
		return nil
	}
	err := rangeValue.file.Close()
	rangeValue.file = nil
	return err
}

type peakRange struct {
	start uint64
	size  uint64
}

func peakRanges(count uint64) []peakRange {
	var ranges []peakRange
	start := uint64(0)
	for bit := highestPowerOfTwo(count); bit > 0; bit >>= 1 {
		if count&bit != 0 {
			ranges = append(ranges, peakRange{start: start, size: bit})
			start += bit
		}
	}
	return ranges
}

func highestPowerOfTwo(value uint64) uint64 {
	if value == 0 {
		return 0
	}
	result := uint64(1)
	for result <= value/2 {
		result <<= 1
	}
	return result
}

func rootFor(leaves []Hash) Hash {
	if len(leaves) == 0 {
		return Sum(nil)
	}
	ranges := peakRanges(uint64(len(leaves)))
	peaks := make([]Hash, len(ranges))
	for index, mountain := range ranges {
		peaks[index] = perfectRoot(leaves, mountain.start, mountain.size)
	}
	return bagPeaks(peaks)
}

func perfectRoot(leaves []Hash, start, size uint64) Hash {
	if size == 1 {
		return leaves[start]
	}
	half := size / 2
	return parentHash(
		perfectRoot(leaves, start, half),
		perfectRoot(leaves, start+half, half),
	)
}

func parentHash(left, right Hash) Hash {
	var input [64]byte
	copy(input[:32], left[:])
	copy(input[32:], right[:])
	return Sum(input[:])
}

func bagPeaks(peaks []Hash) Hash {
	if len(peaks) == 0 {
		return Sum(nil)
	}
	result := peaks[len(peaks)-1]
	for index := len(peaks) - 2; index >= 0; index-- {
		result = parentHash(peaks[index], result)
	}
	return result
}
