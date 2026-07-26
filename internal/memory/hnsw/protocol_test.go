package hnsw

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestClientBinaryProtocolInsertSearchDeleteAndCount(t *testing.T) {
	service := startTestUDSService(t)
	client, err := NewClient(service.path, 3, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	if err := client.Insert(ctx, ^uint64(0), []float32{1, 0, 0}); err != nil {
		t.Fatal(err)
	}
	if err := client.Insert(ctx, 4, []float32{0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	count, err := client.Count(ctx)
	if err != nil || count != 2 {
		t.Fatalf("count = %d, %v", count, err)
	}
	matches, err := client.Search(ctx, []float32{1, 0, 0}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 || matches[0].Key != ^uint64(0) || matches[0].Distance != 0 {
		t.Fatalf("matches = %+v", matches)
	}
	removed, err := client.Delete(ctx, 4)
	if err != nil || !removed {
		t.Fatalf("delete = %v, %v", removed, err)
	}
	removed, err = client.Delete(ctx, 4)
	if err != nil || removed {
		t.Fatalf("second delete = %v, %v", removed, err)
	}
}

func TestClientResetClearsRemoteIndex(t *testing.T) {
	service := startTestUDSService(t)
	client, _ := NewClient(service.path, 2, time.Second)
	defer client.Close()
	_ = client.Insert(context.Background(), 1, []float32{1, 0})
	if err := client.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	count, _ := client.Count(context.Background())
	if count != 0 || service.resets() != 1 {
		t.Fatalf("count/resets = %d/%d", count, service.resets())
	}
}

func TestClientRejectsInvalidVectorsBeforeDial(t *testing.T) {
	client, _ := NewClient(filepath.Join(t.TempDir(), "missing.sock"), 2, time.Second)
	defer client.Close()
	for _, vector := range [][]float32{
		{1},
		{0, 0},
		{float32(math.NaN()), 1},
	} {
		if err := client.Insert(context.Background(), 1, vector); !errors.Is(err, ErrInvalidVector) {
			t.Fatalf("Insert(%v) error = %v", vector, err)
		}
	}
}

func TestClientUnavailableErrorSurvivesSocketCrash(t *testing.T) {
	service := startTestUDSService(t)
	client, _ := NewClient(service.path, 2, time.Second)
	defer client.Close()
	if _, err := client.Count(context.Background()); err != nil {
		t.Fatal(err)
	}
	service.crash()
	if _, err := client.Count(context.Background()); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("post-crash count error = %v", err)
	}
}

func TestClientClosedIsUnavailable(t *testing.T) {
	service := startTestUDSService(t)
	client, _ := NewClient(service.path, 2, time.Second)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Count(context.Background()); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("closed client error = %v", err)
	}
}

func TestNewClientValidatesConfiguration(t *testing.T) {
	if _, err := NewClient("", 2, time.Second); err == nil {
		t.Fatal("empty socket accepted")
	}
	if _, err := NewClient("/tmp/hnsw.sock", 0, time.Second); err == nil {
		t.Fatal("zero dimensions accepted")
	}
}
