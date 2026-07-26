package journal

import (
	"context"
	"fmt"
	"sync"
)

const defaultQueueSize = 64

type appendRequest struct {
	ctx    context.Context
	entry  Entry
	result chan appendResult
}

type appendResult struct {
	record Record
	err    error
}

// Writer owns the single append goroutine for a Journal. Append waits for the
// durable fsync acknowledgement, so callers never observe derived state before
// its source-of-truth mutation is on disk.
type Writer struct {
	source   *Journal
	requests chan appendRequest
	done     chan struct{}

	mu     sync.RWMutex
	closed bool
}

// NewWriter starts a single writer goroutine over source. The Journal remains
// owned by the caller and is not closed by Writer.Close.
func NewWriter(source *Journal) (*Writer, error) {
	if source == nil {
		return nil, fmt.Errorf("journal: source is required")
	}
	writer := &Writer{
		source:   source,
		requests: make(chan appendRequest, defaultQueueSize),
		done:     make(chan struct{}),
	}
	go writer.run()
	return writer, nil
}

// Append queues one mutation and waits until it has been encrypted, appended,
// and fsynced by the writer goroutine.
func (writer *Writer) Append(ctx context.Context, entry Entry) (Record, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	writer.mu.RLock()
	defer writer.mu.RUnlock()
	if writer.closed {
		return Record{}, ErrClosed
	}

	request := appendRequest{
		ctx:    ctx,
		entry:  cloneEntry(entry),
		result: make(chan appendResult, 1),
	}
	select {
	case writer.requests <- request:
	case <-ctx.Done():
		return Record{}, ctx.Err()
	}

	// Once accepted, always collect the durability result. Returning early on
	// cancellation could report failure after the mutation reached disk.
	result := <-request.result
	return result.record, result.err
}

// Close drains accepted requests and stops the writer goroutine.
func (writer *Writer) Close() error {
	writer.mu.Lock()
	if writer.closed {
		writer.mu.Unlock()
		<-writer.done
		return nil
	}
	writer.closed = true
	close(writer.requests)
	writer.mu.Unlock()
	<-writer.done
	return nil
}

func (writer *Writer) run() {
	defer close(writer.done)
	for request := range writer.requests {
		record, err := writer.source.Append(request.ctx, request.entry)
		request.result <- appendResult{record: record, err: err}
	}
}
