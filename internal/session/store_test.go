package session

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/paxlabs-inc/ion-agent/internal/security/safety"
	"github.com/paxlabs-inc/ion-agent/internal/security/vault"
)

const testMaxContextTokens = 1000

var testNow = time.Date(2026, time.July, 18, 12, 0, 0, 123456000, time.UTC)

func TestStoreAppliesRequiredSQLitePragmas(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	checks := map[string]int64{
		"wal_autocheckpoint": 1000,
		"busy_timeout":       5000,
		"synchronous":        1,
		"cache_size":         -8000,
		"mmap_size":          268435456,
		"foreign_keys":       1,
	}
	var journalMode string
	if err := store.readDB.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("journal_mode query error = %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	for pragma, want := range checks {
		pragma := pragma
		want := want
		t.Run(pragma, func(t *testing.T) {
			var got int64
			if err := store.readDB.QueryRow(`PRAGMA ` + pragma).Scan(&got); err != nil {
				t.Fatalf("query error = %v", err)
			}
			if got != want {
				t.Fatalf("%s = %d, want %d", pragma, got, want)
			}
		})
	}
}

func TestStoreMessageRoundTripIsEncryptedAtRest(t *testing.T) {
	t.Parallel()
	store, _, databasePath := openTestStore(t)
	root, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	explicitChild, err := store.CreateSession(context.Background(), &root.ID)
	if err != nil {
		t.Fatalf("CreateSession(explicit child) error = %v", err)
	}
	if explicitChild.ParentID == nil || *explicitChild.ParentID != root.ID {
		t.Fatalf("explicit child ParentID = %v, want %s", explicitChild.ParentID, root.ID)
	}
	secret := []byte("this plaintext must never reach SQLite")
	stored, err := store.AppendMessage(
		context.Background(),
		root.ID,
		RoleUser,
		MemoryTranscript,
		secret,
		100,
	)
	if err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	messages, err := store.ListMessages(context.Background(), root.ID)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 || !bytes.Equal(messages[0].Content, secret) {
		t.Fatalf("messages = %#v", messages)
	}
	if messages[0].ID != stored.ID {
		t.Fatalf("message ID = %s, want %s", messages[0].ID, stored.ID)
	}

	var envelope []byte
	if err := store.readDB.QueryRow(
		`SELECT content FROM messages WHERE id = ?`,
		stored.ID.String(),
	).Scan(&envelope); err != nil {
		t.Fatalf("raw content query error = %v", err)
	}
	if bytes.Equal(envelope, secret) || bytes.Contains(envelope, secret) {
		t.Fatal("SQLite row contains plaintext")
	}
	databaseBytes, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatalf("ReadFile(database) error = %v", err)
	}
	if bytes.Contains(databaseBytes, secret) {
		t.Fatal("database file contains plaintext")
	}
}

func TestCreateScheduledTurnAtomicallyDeduplicatesTranscriptAndState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _, databasePath := openTestStore(t)
	conversation, err := store.CreateSession(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	actorID, turnID, messageID := uuid.New(), uuid.New(), uuid.New()
	content := "scheduled atomic wake content"
	state := TurnState{
		TurnID: turnID, ActorID: actorID, SessionID: conversation.ID,
		Content: content, Surface: "general", Status: TurnRunning,
		UpdatedAt: store.clock.Now(),
	}
	createdState, created, err := store.CreateScheduledTurn(
		ctx, state, messageID, 12,
	)
	if err != nil || !created || createdState.TurnID != turnID {
		t.Fatalf("create scheduled = %+v, created=%t, err=%v", createdState, created, err)
	}
	replayed, created, err := store.CreateScheduledTurn(
		ctx, state, messageID, 12,
	)
	if err != nil || created || replayed.TurnID != turnID {
		t.Fatalf("replay scheduled = %+v, created=%t, err=%v", replayed, created, err)
	}
	messages, err := store.ListMessages(ctx, conversation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID != messageID ||
		string(messages[0].Content) != content {
		t.Fatalf("scheduled messages = %+v", messages)
	}
	database, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(database, []byte(content)) {
		t.Fatal("scheduled turn plaintext reached SQLite")
	}
}

func TestBranchSessionIsAtomicWhenMessageEncryptionFails(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	parent, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	originalCipher := store.cipher
	store.cipher = &failAfterCipher{
		inner: originalCipher,
		after: 1,
		err:   errors.New("second branch encryption failed"),
	}
	_, err = store.BranchSession(context.Background(), parent.ID, []Message{
		{
			Role: RoleUser, MemoryType: MemoryTranscript,
			Content: []byte("first"),
		},
		{
			Role: RoleAssistant, MemoryType: MemoryTranscript,
			Content: []byte("second"),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "second branch encryption failed") {
		t.Fatalf("BranchSession() error = %v", err)
	}
	store.cipher = originalCipher
	sessions, err := store.ListSessions(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != parent.ID {
		t.Fatalf("failed branch left durable state: %+v", sessions)
	}
}

func TestListMessagesPreservesInsertionOrderWhenTimestampsMatch(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	root, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for index, item := range []struct {
		role    Role
		content string
	}{
		{role: RoleUser, content: "first"},
		{role: RoleAssistant, content: "second"},
		{role: RoleTool, content: "third"},
	} {
		if _, err := store.AppendMessage(
			context.Background(),
			root.ID,
			item.role,
			MemoryTranscript,
			[]byte(item.content),
			index+1,
		); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := store.ListMessages(context.Background(), root.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 3 {
		t.Fatalf("message count = %d", len(messages))
	}
	for index, want := range []string{"first", "second", "third"} {
		if string(messages[index].Content) != want {
			t.Fatalf("message %d = %q, want %q", index, messages[index].Content, want)
		}
	}
}

func TestListSessionsReturnsBoundedConversationMetadata(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	first, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateSession(context.Background(), &first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(
		context.Background(),
		second.ID,
		RoleUser,
		MemoryTranscript,
		[]byte("encrypted conversation preview"),
		17,
	); err != nil {
		t.Fatal(err)
	}

	found, err := store.ListSessions(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("session count = %d, want 2", len(found))
	}
	byID := make(map[uuid.UUID]Session, len(found))
	for _, item := range found {
		byID[item.ID] = item
	}
	child, exists := byID[second.ID]
	if !exists || child.ParentID == nil || *child.ParentID != first.ID {
		t.Fatalf("listed child = %+v", child)
	}
	if child.ContextTokens != 17 {
		t.Fatalf("listed context tokens = %d, want 17", child.ContextTokens)
	}
	for _, limit := range []int{0, maxSessionListLimit + 1} {
		if _, err := store.ListSessions(context.Background(), limit); err == nil {
			t.Fatalf("ListSessions(limit=%d) succeeded", limit)
		}
	}
}

func TestConversationRenameArchiveRestoreAndExactDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _, databasePath := openTestStore(t)
	parent, err := store.CreateSession(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.CreateSession(ctx, &parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := store.RenameSession(ctx, parent.ID, "  Release   planning  ")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Title != "Release planning" {
		t.Fatalf("renamed title = %q", renamed.Title)
	}
	databaseBytes, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(databaseBytes, []byte("Release planning")) {
		t.Fatal("conversation title reached SQLite as plaintext")
	}
	archived, err := store.ArchiveSession(ctx, parent.ID, true)
	if err != nil || archived.ArchivedAt == nil {
		t.Fatalf("archive = %+v, %v", archived, err)
	}
	active, err := store.ListSessions(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range active {
		if item.ID == parent.ID {
			t.Fatal("archived conversation remained in active list")
		}
	}
	archivedSessions, err := store.ListArchivedSessions(ctx, 10)
	if err != nil || len(archivedSessions) != 1 ||
		archivedSessions[0].ID != parent.ID {
		t.Fatalf("archived sessions = %+v, %v", archivedSessions, err)
	}
	restored, err := store.ArchiveSession(ctx, parent.ID, false)
	if err != nil || restored.ArchivedAt != nil {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	if err := store.DeleteSession(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetSession(ctx, parent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted parent lookup = %v", err)
	}
	detached, err := store.GetSession(ctx, child.ID)
	if err != nil || detached.ParentID != nil {
		t.Fatalf("detached child = %+v, %v", detached, err)
	}
}

func TestAppendTurnMessageRestoresTurnCorrelation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, _, _ := openTestStore(t)
	conversation, err := store.CreateSession(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	turnID := uuid.New()
	if _, err := store.AppendTurnMessage(
		ctx,
		conversation.ID,
		turnID,
		RoleAssistant,
		MemorySummary,
		[]byte("Reviewing the request and available context."),
		12,
	); err != nil {
		t.Fatal(err)
	}
	messages, err := store.ListMessages(ctx, conversation.ID)
	if err != nil || len(messages) != 1 ||
		messages[0].TurnID == nil || *messages[0].TurnID != turnID {
		t.Fatalf("turn messages = %+v, %v", messages, err)
	}
}

func TestStoreFTS5SearchesOnlyMetadata(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	columnRows, err := store.readDB.Query(`PRAGMA table_info(message_metadata_fts)`)
	if err != nil {
		t.Fatalf("FTS table_info query error = %v", err)
	}
	var columns []string
	for columnRows.Next() {
		var (
			index       int
			name        string
			columnType  string
			notNull     int
			defaultExpr sql.NullString
			primaryKey  int
		)
		if err := columnRows.Scan(
			&index,
			&name,
			&columnType,
			&notNull,
			&defaultExpr,
			&primaryKey,
		); err != nil {
			_ = columnRows.Close()
			t.Fatalf("FTS table_info scan error = %v", err)
		}
		columns = append(columns, name)
	}
	if err := columnRows.Err(); err != nil {
		_ = columnRows.Close()
		t.Fatalf("FTS table_info iteration error = %v", err)
	}
	if err := columnRows.Close(); err != nil {
		t.Fatalf("FTS table_info close error = %v", err)
	}
	wantColumns := []string{"message_id", "session_id", "memory_type", "created_at"}
	if fmt.Sprint(columns) != fmt.Sprint(wantColumns) {
		t.Fatalf("FTS columns = %v, want %v", columns, wantColumns)
	}

	root, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	secret := []byte("unindexed-platypus-phrase")
	stored, err := store.AppendMessage(
		context.Background(),
		root.ID,
		RoleAssistant,
		MemorySummary,
		secret,
		100,
	)
	if err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	results, err := store.SearchMetadataInSession(context.Background(), root.ID, "summary", 10)
	if err != nil {
		t.Fatalf("SearchMetadata(memory type) error = %v", err)
	}
	if len(results) != 1 || !bytes.Equal(results[0].Content, secret) {
		t.Fatalf("memory type results = %#v", results)
	}
	results, err = store.SearchMetadataInSession(
		context.Background(),
		root.ID,
		fmt.Sprintf("%q", stored.ID.String()),
		10,
	)
	if err != nil {
		t.Fatalf("SearchMetadata(message ID) error = %v", err)
	}
	if len(results) != 1 || results[0].ID != stored.ID {
		t.Fatalf("message ID results = %#v", results)
	}
	results, err = store.SearchMetadataInSession(context.Background(), root.ID, "assistant", 10)
	if err != nil {
		t.Fatalf("SearchMetadata(role) error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("role unexpectedly indexed: %#v", results)
	}
	results, err = store.SearchMetadataInSession(context.Background(), root.ID, "platypus", 10)
	if err != nil {
		t.Fatalf("SearchMetadata(plaintext) error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("plaintext unexpectedly indexed: %#v", results)
	}

	var indexedText sql.NullString
	if err := store.readDB.QueryRow(
		`SELECT group_concat(
		    coalesce(message_id, '') || coalesce(session_id, '') ||
		    coalesce(memory_type, '') || coalesce(created_at, ''),
		    ' '
		 ) FROM message_metadata_fts`,
	).Scan(&indexedText); err != nil {
		t.Fatalf("FTS content query error = %v", err)
	}
	if bytes.Contains([]byte(indexedText.String), secret) {
		t.Fatal("FTS table contains plaintext content")
	}
}

func TestStoreMetadataSearchCannotLeakAcrossSessionBoundary(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	first, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(
		context.Background(),
		first.ID,
		RoleUser,
		MemorySummary,
		[]byte("first-session-secret"),
		1,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendMessage(
		context.Background(),
		second.ID,
		RoleUser,
		MemorySummary,
		[]byte("second-session-secret"),
		1,
	); err != nil {
		t.Fatal(err)
	}
	results, err := store.SearchMetadataInSession(
		context.Background(), first.ID, "summary", 10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].SessionID != first.ID ||
		string(results[0].Content) != "first-session-secret" {
		t.Fatalf("scoped search leaked or lost data: %+v", results)
	}
}

func TestStoreCrossSessionMetadataSearchUsesExplicitAllowlist(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	first, _ := store.CreateSession(context.Background(), nil)
	second, _ := store.CreateSession(context.Background(), nil)
	third, _ := store.CreateSession(context.Background(), nil)
	for index, id := range []uuid.UUID{first.ID, second.ID, third.ID} {
		if _, err := store.AppendMessage(
			context.Background(), id, RoleUser, MemorySummary,
			[]byte(fmt.Sprintf("session-%d-secret", index+1)), 1,
		); err != nil {
			t.Fatal(err)
		}
	}
	results, err := store.SearchMetadataAcrossSessions(
		context.Background(),
		[]uuid.UUID{first.ID, second.ID, first.ID},
		"summary",
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("allowlisted results = %+v", results)
	}
	for _, result := range results {
		if result.SessionID == third.ID {
			t.Fatal("cross-session search leaked an unauthorized session")
		}
	}
	if _, err := store.SearchMetadataAcrossSessions(
		context.Background(), nil, "summary", 10,
	); err == nil {
		t.Fatal("empty cross-session authorization succeeded")
	}
}

func TestStoreCompressionTriggeredSplitLinksChild(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	root, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	beforeThreshold, err := store.AppendMessage(
		context.Background(),
		root.ID,
		RoleUser,
		MemoryTranscript,
		[]byte("before"),
		749,
	)
	if err != nil {
		t.Fatalf("AppendMessage(before) error = %v", err)
	}
	if beforeThreshold.SessionID != root.ID {
		t.Fatal("session split before 75% threshold")
	}
	atThreshold, err := store.AppendMessage(
		context.Background(),
		root.ID,
		RoleAssistant,
		MemorySummary,
		[]byte("compressed summary"),
		750,
	)
	if err != nil {
		t.Fatalf("AppendMessage(at threshold) error = %v", err)
	}
	if atThreshold.SessionID == root.ID {
		t.Fatal("session did not split at 75% threshold")
	}
	child, err := store.GetSession(context.Background(), atThreshold.SessionID)
	if err != nil {
		t.Fatalf("GetSession(child) error = %v", err)
	}
	if child.ParentID == nil || *child.ParentID != root.ID {
		t.Fatalf("child ParentID = %v, want %s", child.ParentID, root.ID)
	}
	childMessages, err := store.ListMessages(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("ListMessages(child) error = %v", err)
	}
	if len(childMessages) != 1 || string(childMessages[0].Content) != "compressed summary" {
		t.Fatalf("child messages = %#v", childMessages)
	}
}

func TestStoreConcurrentReadDoesNotBlockSingleWriter(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	root, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if _, err := store.AppendMessage(
		context.Background(),
		root.ID,
		RoleUser,
		MemoryTranscript,
		[]byte("existing"),
		10,
	); err != nil {
		t.Fatalf("AppendMessage(existing) error = %v", err)
	}

	readTx, err := store.readDB.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx(read) error = %v", err)
	}
	defer func() {
		// Best-effort rollback after the concurrency assertion.
		_ = readTx.Rollback()
	}()
	var count int
	if err := readTx.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count); err != nil {
		t.Fatalf("held read query error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, appendErr := store.AppendMessage(
			context.Background(),
			root.ID,
			RoleAssistant,
			MemoryTranscript,
			[]byte("writer proceeds under WAL"),
			20,
		)
		done <- appendErr
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("concurrent AppendMessage() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("single writer was blocked by an active reader")
	}
}

func TestStoreConcurrentReadersAndWriter(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	root, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if _, err := store.AppendMessage(
		context.Background(),
		root.ID,
		RoleUser,
		MemoryTranscript,
		[]byte("seed"),
		1,
	); err != nil {
		t.Fatalf("AppendMessage(seed) error = %v", err)
	}

	start := make(chan struct{})
	errorsFound := make(chan error, 9)
	var wait sync.WaitGroup
	for reader := 0; reader < 8; reader++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for iteration := 0; iteration < 20; iteration++ {
				messages, readErr := store.ListMessages(context.Background(), root.ID)
				if readErr != nil {
					errorsFound <- readErr
					return
				}
				if len(messages) == 0 {
					errorsFound <- fmt.Errorf("reader observed no messages")
					return
				}
			}
		}()
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		<-start
		for iteration := 0; iteration < 20; iteration++ {
			if _, writeErr := store.AppendMessage(
				context.Background(),
				root.ID,
				RoleAssistant,
				MemoryTranscript,
				[]byte(fmt.Sprintf("message-%d", iteration)),
				iteration+2,
			); writeErr != nil {
				errorsFound <- writeErr
				return
			}
		}
	}()
	close(start)
	wait.Wait()
	close(errorsFound)
	for concurrentErr := range errorsFound {
		t.Errorf("concurrent operation error = %v", concurrentErr)
	}
}

func TestStoreUserKeyRotationRewrapsRows(t *testing.T) {
	t.Parallel()
	store, instance, _ := openTestStore(t)
	root, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	for _, content := range []string{"first", "second", "third"} {
		if _, err := store.AppendMessage(
			context.Background(),
			root.ID,
			RoleUser,
			MemoryTranscript,
			[]byte(content),
			10,
		); err != nil {
			t.Fatalf("AppendMessage() error = %v", err)
		}
	}
	var oldEnvelope []byte
	if err := store.readDB.QueryRow(`SELECT content FROM messages ORDER BY id LIMIT 1`).Scan(&oldEnvelope); err != nil {
		t.Fatalf("raw query error = %v", err)
	}
	if err := instance.RotateUserKey(context.Background(), store); err != nil {
		t.Fatalf("RotateUserKey() error = %v", err)
	}
	var newEnvelope []byte
	if err := store.readDB.QueryRow(`SELECT content FROM messages ORDER BY id LIMIT 1`).Scan(&newEnvelope); err != nil {
		t.Fatalf("rotated raw query error = %v", err)
	}
	if bytes.Equal(oldEnvelope, newEnvelope) {
		t.Fatal("encrypted DEK did not change")
	}
	messages, err := store.ListMessages(context.Background(), root.ID)
	if err != nil {
		t.Fatalf("ListMessages() after rotation error = %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("message count = %d", len(messages))
	}
}

func TestStoreManagerUserKeyRotationSurvivesRestart(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	source, err := vault.NewFileKEKSource(filepath.Join(directory, "kek"))
	if err != nil {
		t.Fatalf("NewFileKEKSource() error = %v", err)
	}
	keyStore, err := vault.NewFileWrappedKeyStore(filepath.Join(directory, "user.enc"))
	if err != nil {
		t.Fatalf("NewFileWrappedKeyStore() error = %v", err)
	}
	manager, err := vault.Initialize(context.Background(), source, keyStore)
	if err != nil {
		t.Fatalf("vault.Initialize() error = %v", err)
	}
	databasePath := filepath.Join(directory, "sessions.db")
	first, err := Open(
		context.Background(),
		databasePath,
		manager.Vault(),
		fixedClock{},
		testMaxContextTokens,
	)
	if err != nil {
		t.Fatalf("Open(first) error = %v", err)
	}
	root, err := first.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	if _, err := first.AppendMessage(
		context.Background(),
		root.ID,
		RoleUser,
		MemoryTranscript,
		[]byte("survives process restart"),
		10,
	); err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	if err := manager.RotateUserKey(context.Background(), first); err != nil {
		t.Fatalf("Manager.RotateUserKey() error = %v", err)
	}
	closeStore(t, first)
	if err := manager.Close(); err != nil {
		t.Fatalf("Manager.Close() error = %v", err)
	}

	reopenedManager, err := vault.Open(context.Background(), source, keyStore)
	if err != nil {
		t.Fatalf("vault.Open() error = %v", err)
	}
	defer func() {
		if err := reopenedManager.Close(); err != nil {
			t.Errorf("reopened Manager.Close() error = %v", err)
		}
	}()
	second, err := Open(
		context.Background(),
		databasePath,
		reopenedManager.Vault(),
		fixedClock{},
		testMaxContextTokens,
	)
	if err != nil {
		t.Fatalf("Open(second) error = %v", err)
	}
	defer closeStore(t, second)
	messages, err := second.ListMessages(context.Background(), root.ID)
	if err != nil {
		t.Fatalf("ListMessages(after restart) error = %v", err)
	}
	if len(messages) != 1 || string(messages[0].Content) != "survives process restart" {
		t.Fatalf("messages after restart = %#v", messages)
	}
}

func TestStoreMigrationsAreIdempotent(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "sessions.db")
	instance, err := vault.New(bytes.Repeat([]byte{0x91}, vault.KeySize))
	if err != nil {
		t.Fatalf("vault.New() error = %v", err)
	}
	defer func() {
		if err := instance.Close(); err != nil {
			t.Errorf("Vault.Close() error = %v", err)
		}
	}()
	first, err := Open(context.Background(), databasePath, instance, fixedClock{}, testMaxContextTokens)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	closeStore(t, first)
	second, err := Open(context.Background(), databasePath, instance, fixedClock{}, testMaxContextTokens)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer closeStore(t, second)
	var count int
	if err := second.readDB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("migration count query error = %v", err)
	}
	if count != 6 {
		t.Fatalf("migration count = %d, want 6", count)
	}
}

func TestStoreValidationAndNotFound(t *testing.T) {
	t.Parallel()
	instance, err := vault.New(bytes.Repeat([]byte{0x92}, vault.KeySize))
	if err != nil {
		t.Fatalf("vault.New() error = %v", err)
	}
	defer func() {
		if err := instance.Close(); err != nil {
			t.Errorf("Vault.Close() error = %v", err)
		}
	}()
	for name, testCase := range rangeOpenCases(instance) {
		name := name
		testCase := testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if store, err := Open(
				context.Background(),
				testCase.path,
				testCase.cipher,
				testCase.clock,
				testCase.maximum,
			); err == nil {
				closeStore(t, store)
				t.Fatal("Open() succeeded")
			}
		})
	}

	store, _, _ := openTestStore(t)
	if _, err := store.GetSession(context.Background(), uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSession(missing) error = %v", err)
	}
	if _, err := store.SearchMetadataInSession(context.Background(), uuid.New(), "", 1); err == nil {
		t.Fatal("SearchMetadataInSession(empty) succeeded")
	}
	if _, err := store.SearchMetadataInSession(context.Background(), uuid.New(), "user", 0); err == nil {
		t.Fatal("SearchMetadataInSession(zero limit) succeeded")
	}
}

func TestStoreCloseIsIdempotentAndRejectsOperations(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	instance, err := vault.New(bytes.Repeat([]byte{0x93}, vault.KeySize))
	if err != nil {
		t.Fatalf("vault.New() error = %v", err)
	}
	defer func() {
		if err := instance.Close(); err != nil {
			t.Errorf("Vault.Close() error = %v", err)
		}
	}()
	store, err := Open(
		context.Background(),
		filepath.Join(directory, "sessions.db"),
		instance,
		fixedClock{},
		testMaxContextTokens,
	)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	closeStore(t, store)
	closeStore(t, store)
	if _, err := store.CreateSession(context.Background(), nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("CreateSession(closed) error = %v", err)
	}
	if _, err := store.GetSession(context.Background(), uuid.New()); !errors.Is(err, ErrClosed) {
		t.Fatalf("GetSession(closed) error = %v", err)
	}
}

func TestStoreRejectsInvalidMessageInputs(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	root, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	for name, testCase := range rangeInvalidMessageCases() {
		name := name
		testCase := testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := store.AppendMessage(
				context.Background(),
				root.ID,
				testCase.role,
				testCase.memoryType,
				testCase.content,
				testCase.tokens,
			); err == nil {
				t.Fatal("AppendMessage() succeeded")
			}
		})
	}
	if _, err := store.CreateSession(context.Background(), pointerToUUID(uuid.New())); err == nil {
		t.Fatal("CreateSession(missing parent) succeeded")
	}
}

func TestStoreSurfacesEncryptedRowCorruption(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	root, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	message, err := store.AppendMessage(
		context.Background(),
		root.ID,
		RoleUser,
		MemoryTranscript,
		[]byte("authenticated"),
		1,
	)
	if err != nil {
		t.Fatalf("AppendMessage() error = %v", err)
	}
	if _, err := store.writeDB.Exec(
		`UPDATE messages SET content = zeroblob(length(content)) WHERE id = ?`,
		message.ID.String(),
	); err != nil {
		t.Fatalf("corrupt row error = %v", err)
	}
	if _, err := store.ListMessages(context.Background(), root.ID); !errors.Is(err, vault.ErrDecryptionFailed) {
		t.Fatalf("ListMessages(corrupt) error = %v", err)
	}
	if err := store.RewrapEnvelopes(
		context.Background(),
		bytes.Repeat([]byte{0x90}, vault.KeySize),
		bytes.Repeat([]byte{0x94}, vault.KeySize),
	); !errors.Is(err, vault.ErrDecryptionFailed) {
		t.Fatalf("RewrapEnvelopes(corrupt) error = %v", err)
	}
}

func TestStorePropagatesCipherAndFTSErrors(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	root, err := store.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}
	expected := errors.New("cipher unavailable")
	originalCipher := store.cipher
	store.cipher = failingCipher{err: expected}
	if _, err := store.AppendMessage(
		context.Background(),
		root.ID,
		RoleUser,
		MemoryTranscript,
		[]byte("not stored"),
		1,
	); !errors.Is(err, expected) {
		t.Fatalf("AppendMessage(cipher failure) error = %v", err)
	}
	store.cipher = originalCipher
	if _, err := store.SearchMetadataInSession(context.Background(), root.ID, `"`, 1); err == nil {
		t.Fatal("SearchMetadataInSession(malformed FTS query) succeeded")
	}
}

func TestBusyRetryRetriesAndHonorsCancellation(t *testing.T) {
	t.Parallel()
	databasePath := filepath.Join(t.TempDir(), "busy.db")
	lockDB, err := sql.Open("sqlite", mustSQLiteDSN(t, databasePath))
	if err != nil {
		t.Fatalf("sql.Open(lock) error = %v", err)
	}
	defer func() {
		_ = lockDB.Close() // Best-effort test cleanup.
	}()
	contenderDB, err := sql.Open("sqlite", mustSQLiteDSN(t, databasePath))
	if err != nil {
		t.Fatalf("sql.Open(contender) error = %v", err)
	}
	defer func() {
		_ = contenderDB.Close() // Best-effort test cleanup.
	}()
	if _, err := lockDB.Exec(`CREATE TABLE busy_test (value INTEGER)`); err != nil {
		t.Fatalf("CREATE TABLE error = %v", err)
	}
	lock, err := lockDB.Begin()
	if err != nil {
		t.Fatalf("Begin(lock) error = %v", err)
	}
	if _, err := lock.Exec(`INSERT INTO busy_test(value) VALUES (1)`); err != nil {
		t.Fatalf("INSERT(lock) error = %v", err)
	}
	defer func() {
		_ = lock.Rollback() // Best-effort test cleanup.
	}()

	var busyErr error
	_, busyErr = contenderDB.Exec(`INSERT INTO busy_test(value) VALUES (2)`)
	if !isSQLiteBusy(busyErr) {
		t.Fatalf("contending insert error = %v, want SQLITE_BUSY", busyErr)
	}
	if isSQLiteBusy(errors.New("ordinary")) {
		t.Fatal("ordinary error classified as SQLITE_BUSY")
	}

	attempts := 0
	result := runWithBusyRetry(context.Background(), contenderDB, func(context.Context, *sql.DB) writeResult {
		attempts++
		if attempts < 3 {
			return writeResult{err: busyErr}
		}
		return writeResult{}
	})
	if result.err != nil || attempts != 3 {
		t.Fatalf("runWithBusyRetry() result = %v after %d attempts", result.err, attempts)
	}

	attempts = 0
	result = runWithBusyRetry(context.Background(), contenderDB, func(context.Context, *sql.DB) writeResult {
		attempts++
		return writeResult{err: busyErr}
	})
	if !isSQLiteBusy(result.err) || attempts != maxBusyRetries+1 {
		t.Fatalf("exhausted retry result = %v after %d attempts", result.err, attempts)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result = runWithBusyRetry(ctx, contenderDB, func(context.Context, *sql.DB) writeResult {
		return writeResult{err: busyErr}
	})
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("runWithBusyRetry(cancelled) error = %v", result.err)
	}
}

func TestNewMessageValidation(t *testing.T) {
	t.Parallel()
	validID := uuid.New()
	cases := map[string]struct {
		sessionID  uuid.UUID
		role       Role
		memoryType MemoryType
		content    []byte
		now        time.Time
	}{
		"nil_session":   {uuid.Nil, RoleUser, MemoryTranscript, []byte("x"), testNow},
		"invalid_role":  {validID, Role("invalid"), MemoryTranscript, []byte("x"), testNow},
		"invalid_type":  {validID, RoleUser, MemoryType("invalid"), []byte("x"), testNow},
		"empty_content": {validID, RoleUser, MemoryTranscript, nil, testNow},
		"zero_time":     {validID, RoleUser, MemoryTranscript, []byte("x"), time.Time{}},
	}
	for name, testCase := range cases {
		testCase := testCase
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewMessage(
				testCase.sessionID,
				testCase.role,
				testCase.memoryType,
				testCase.content,
				testCase.now,
			); err == nil {
				t.Fatal("NewMessage() succeeded")
			}
		})
	}
	for _, role := range []Role{RoleSystem, RoleUser, RoleAssistant, RoleTool} {
		if !role.Valid() {
			t.Fatalf("valid role %q rejected", role)
		}
	}
	for _, memoryType := range []MemoryType{MemoryTranscript, MemorySummary, MemoryToolEvent} {
		if !memoryType.Valid() {
			t.Fatalf("valid memory type %q rejected", memoryType)
		}
	}
}

func TestEmotionalStatePersistsEncryptedAcrossSessions(t *testing.T) {
	t.Parallel()
	store, _, _ := openTestStore(t)
	state := safety.NewEmotionalState()
	state.UpdateAll(safety.EmotionalSnapshot{
		Frustration: 0.7, Confidence: 0.8, Urgency: 0.3,
		Satisfaction: 0.9, Curiosity: 0.6, Fatigue: 0.2,
		UpdatedAt: testNow,
	})
	if err := store.SaveEmotionalState(
		context.Background(),
		"user-a",
		state,
	); err != nil {
		t.Fatal(err)
	}
	var envelope []byte
	if err := store.readDB.QueryRow(
		`SELECT state FROM emotional_state WHERE user_id = ?`,
		"user-a",
	).Scan(&envelope); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(envelope, []byte(`"frustration"`)) {
		t.Fatal("emotional state was stored as plaintext")
	}
	loaded, err := store.LoadEmotionalState(context.Background(), "user-a")
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.FullSnapshot()
	if got.Frustration != 0.7 || got.Confidence != 0.8 ||
		got.Urgency != 0.3 || got.Satisfaction != 0.9 ||
		got.Curiosity != 0.6 || got.Fatigue != 0.2 {
		t.Fatalf("loaded emotional state = %+v", got)
	}
}

func openTestStore(t *testing.T) (*Store, *vault.Vault, string) {
	t.Helper()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "sessions.db")
	instance, err := vault.New(bytes.Repeat([]byte{0x90}, vault.KeySize))
	if err != nil {
		t.Fatalf("vault.New() error = %v", err)
	}
	store, err := Open(
		context.Background(),
		databasePath,
		instance,
		fixedClock{},
		testMaxContextTokens,
	)
	if err != nil {
		_ = instance.Close() // Best-effort test cleanup after failed open.
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		closeStore(t, store)
		if err := instance.Close(); err != nil {
			t.Errorf("Vault.Close() error = %v", err)
		}
	})
	return store, instance, databasePath
}

func closeStore(t *testing.T, store *Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := store.Close(ctx); err != nil {
		t.Errorf("Store.Close() error = %v", err)
	}
}

type fixedClock struct{}

func (fixedClock) Now() time.Time {
	return testNow
}

type failingCipher struct {
	err error
}

func (cipher failingCipher) Encrypt([]byte) ([]byte, error) {
	return nil, cipher.err
}

func (cipher failingCipher) Decrypt([]byte) ([]byte, error) {
	return nil, cipher.err
}

type failAfterCipher struct {
	inner Cipher
	after int
	calls int
	err   error
}

func (cipher *failAfterCipher) Encrypt(value []byte) ([]byte, error) {
	if cipher.calls >= cipher.after {
		return nil, cipher.err
	}
	cipher.calls++
	return cipher.inner.Encrypt(value)
}

func (cipher *failAfterCipher) Decrypt(value []byte) ([]byte, error) {
	return cipher.inner.Decrypt(value)
}

func rangeOpenCases(instance *vault.Vault) map[string]struct {
	path    string
	cipher  Cipher
	clock   interface{ Now() time.Time }
	maximum int
} {
	return map[string]struct {
		path    string
		cipher  Cipher
		clock   interface{ Now() time.Time }
		maximum int
	}{
		"empty_path":   {"", instance, fixedClock{}, 1},
		"nil_cipher":   {"ignored.db", nil, fixedClock{}, 1},
		"nil_clock":    {"ignored.db", instance, nil, 1},
		"zero_maximum": {"ignored.db", instance, fixedClock{}, 0},
	}
}

func rangeInvalidMessageCases() map[string]struct {
	role       Role
	memoryType MemoryType
	content    []byte
	tokens     int
} {
	return map[string]struct {
		role       Role
		memoryType MemoryType
		content    []byte
		tokens     int
	}{
		"negative_tokens": {RoleUser, MemoryTranscript, []byte("x"), -1},
		"invalid_role":    {Role("invalid"), MemoryTranscript, []byte("x"), 1},
		"invalid_type":    {RoleUser, MemoryType("invalid"), []byte("x"), 1},
		"empty_content":   {RoleUser, MemoryTranscript, nil, 1},
	}
}

func pointerToUUID(id uuid.UUID) *uuid.UUID {
	return &id
}

func mustSQLiteDSN(t *testing.T, path string) string {
	t.Helper()
	dsn, err := sqliteDSN(path)
	if err != nil {
		t.Fatalf("sqliteDSN() error = %v", err)
	}
	return dsn + "&_pragma=busy_timeout%281%29"
}
