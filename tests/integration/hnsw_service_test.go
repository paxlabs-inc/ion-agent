//go:build integration

package integration

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/paxlabs-inc/ion-agent/internal/memory/hnsw"
)

func TestHNSWServiceTenThousandCrashFallbackAndRebuild(t *testing.T) {
	binary := os.Getenv("ION_HNSW_BINARY")
	if binary == "" {
		binary = filepath.Join("..", "..", "hnsw-service", "target", "debug", "ion-hnsw")
	}
	absoluteBinary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(absoluteBinary); errors.Is(err, os.ErrNotExist) {
		t.Skip("Rust HNSW binary is not built")
	} else if err != nil {
		t.Fatal(err)
	}

	const dimensions = 32
	ctx := context.Background()
	socket := filepath.Join(t.TempDir(), "hnsw.sock")
	process := startHNSWProcess(t, absoluteBinary, socket, dimensions)
	client, err := hnsw.NewClient(socket, dimensions, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	store, err := hnsw.OpenSQLiteStore(
		ctx,
		filepath.Join(t.TempDir(), "cortex-vectors.db"),
		dimensions,
	)
	if err != nil {
		t.Fatal(err)
	}
	index, err := hnsw.NewIndex(ctx, hnsw.Config{
		Remote:     client,
		Store:      store,
		Dimensions: dimensions,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	var query []float32
	for key := 1; key <= 10_000; key++ {
		vector := deterministicVector(uint64(key), dimensions)
		if key == 7_777 {
			query = append([]float32(nil), vector...)
		}
		if err := index.Upsert(ctx, uint64(key), vector); err != nil {
			t.Fatalf("Upsert(%d): %v", key, err)
		}
	}
	matches, err := index.Search(ctx, query, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 10 || matches[0].Key != 7_777 {
		t.Fatalf("top ten = %+v", matches)
	}

	assertSearchP99(t, index, query, 10, 5*time.Millisecond)
	assertSearchP99(t, index, query, 100, 20*time.Millisecond)

	process.crash(t)
	matches, err = index.Search(ctx, query, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 10 || matches[0].Key != 7_777 || !index.Degraded() {
		t.Fatalf("fallback top ten/degraded = %+v/%v", matches, index.Degraded())
	}
	if err := index.Upsert(ctx, 10_001, deterministicVector(10_001, dimensions)); err != nil {
		t.Fatal(err)
	}

	restartedSocket := filepath.Join(t.TempDir(), "hnsw-restarted.sock")
	restartedProcess := startHNSWProcess(t, absoluteBinary, restartedSocket, dimensions)
	defer restartedProcess.crash(t)
	restartedClient, _ := hnsw.NewClient(restartedSocket, dimensions, 2*time.Second)
	restarted, err := hnsw.NewIndex(ctx, hnsw.Config{
		Remote:     restartedClient,
		Store:      store,
		Dimensions: dimensions,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if !restarted.RebuiltOnStartup() {
		t.Fatal("empty restarted service was not rebuilt from durable Cortex vectors")
	}
	count, err := restartedClient.Count(ctx)
	if err != nil || count != 10_001 {
		t.Fatalf("rebuilt count = %d, %v", count, err)
	}
}

func assertSearchP99(
	t *testing.T,
	index *hnsw.Index,
	query []float32,
	k int,
	budget time.Duration,
) {
	t.Helper()
	const samples = 200
	durations := make([]time.Duration, samples)
	for sample := range durations {
		started := time.Now()
		if _, err := index.Search(context.Background(), query, k); err != nil {
			t.Fatal(err)
		}
		durations[sample] = time.Since(started)
	}
	sort.Slice(durations, func(left, right int) bool {
		return durations[left] < durations[right]
	})
	p99 := durations[(samples*99+99)/100-1]
	if p99 >= budget {
		t.Fatalf("search k=%d p99 %s exceeds %s", k, p99, budget)
	}
}

type hnswProcess struct {
	command *exec.Cmd
}

func startHNSWProcess(
	t *testing.T,
	binary string,
	socket string,
	dimensions int,
) *hnswProcess {
	t.Helper()
	command := exec.Command(
		binary,
		"--socket", socket,
		"--dimensions", strconv.Itoa(dimensions),
		"--capacity", "10000",
	)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	process := &hnswProcess{command: command}
	t.Cleanup(func() { process.crash(t) })
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			return process
		}
		if time.Now().After(deadline) {
			process.crash(t)
			t.Fatal("timed out waiting for HNSW socket")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (process *hnswProcess) crash(t *testing.T) {
	t.Helper()
	if process == nil || process.command == nil || process.command.Process == nil {
		return
	}
	if process.command.ProcessState != nil {
		return
	}
	if err := process.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("kill HNSW process: %v", err)
	}
	_ = process.command.Wait()
}

func deterministicVector(key uint64, dimensions int) []float32 {
	state := key ^ 0x9e37_79b9_7f4a_7c15
	vector := make([]float32, dimensions)
	var squared float64
	for index := range vector {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		value := float32(int32(state>>40) - (1 << 23))
		vector[index] = value
		squared += float64(value) * float64(value)
	}
	norm := float32(math.Sqrt(squared))
	for index := range vector {
		vector[index] /= norm
	}
	return vector
}
