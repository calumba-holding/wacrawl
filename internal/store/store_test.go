package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreReplaceStatusListSearch(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	stats := ImportStats{SourcePath: "/tmp/source", DBPath: st.Path(), StartedAt: now.Add(-time.Second), FinishedAt: now}
	contacts := []Contact{{JID: "alice@s.whatsapp.net", FullName: "Alice", UpdatedAt: now}}
	chats := []Chat{{JID: "chat@g.us", Kind: "group", Name: "Chat", LastMessageAt: now, MessageCount: 2}}
	groups := []Group{{JID: "chat@g.us", Name: "Chat", OwnerJID: "owner@s.whatsapp.net", CreatedAt: now.Add(-time.Hour)}}
	participants := []GroupParticipant{{GroupJID: "chat@g.us", UserJID: "alice@s.whatsapp.net", ContactName: "Alice", IsAdmin: true, IsActive: true}}
	messages := []Message{
		{SourcePK: 1, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "a", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice", Timestamp: now.Add(-time.Minute), Text: "hello launch", RawType: 0, MessageType: "text"},
		{SourcePK: 2, ChatJID: "chat@g.us", ChatName: "Chat", MessageID: "b", SenderJID: "me", SenderName: "me", Timestamp: now, FromMe: true, Text: "photo", RawType: 1, MessageType: "image", MediaType: "image", MediaTitle: "launch image", MediaPath: "/tmp/image.jpg", ArchivedMediaPath: "media/imports/one/image.jpg", MediaSize: 123},
	}
	if err := st.ReplaceAll(ctx, stats, contacts, chats, groups, participants, messages); err != nil {
		t.Fatal(err)
	}

	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Messages != 2 || status.MediaMessages != 1 || status.LastSource != "/tmp/source" {
		t.Fatalf("unexpected status: %+v", status)
	}
	if st.DB() == nil {
		t.Fatal("DB should be available")
	}

	listed, err := st.Messages(ctx, MessageFilter{ChatJID: "chat@g.us", Limit: 10, Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].MessageID != "a" || listed[1].MessageID != "b" {
		t.Fatalf("unexpected messages: %+v", listed)
	}

	onlyMine := true
	filtered, err := st.Messages(ctx, MessageFilter{FromMe: &onlyMine, HasMedia: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].MessageID != "b" || filtered[0].ArchivedMediaPath != "media/imports/one/image.jpg" {
		t.Fatalf("unexpected filtered messages: %+v", filtered)
	}

	results, err := st.Search(ctx, MessageFilter{Query: "launch", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 search results, got %d", len(results))
	}
	if _, err := st.Search(ctx, MessageFilter{}); err == nil {
		t.Fatal("expected empty search query error")
	}

	after := now.Add(-2 * time.Minute)
	before := now.Add(time.Minute)
	results, err = st.Messages(ctx, MessageFilter{After: &after, Before: &before, Sender: "alice@s.whatsapp.net", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].MessageID != "a" {
		t.Fatalf("unexpected ranged sender results: %+v", results)
	}

	chatsOut, err := st.ListChats(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(chatsOut) != 1 || chatsOut[0].JID != "chat@g.us" {
		t.Fatalf("unexpected chats: %+v", chatsOut)
	}
}

func TestOpenMigratesArchivedMediaPathColumn(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table messages (
		rowid integer primary key autoincrement,
		source_pk integer not null unique,
		chat_jid text not null,
		chat_name text,
		msg_id text not null,
		sender_jid text,
		sender_name text,
		ts integer not null,
		from_me integer not null,
		text text,
		raw_type integer not null,
		message_type text,
		media_type text,
		media_title text,
		media_path text,
		media_url text,
		media_size integer,
		starred integer not null default 0
	)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var exists int
	if err := st.DB().QueryRow(`select count(*) from pragma_table_info('messages') where name='archived_media_path'`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != 1 {
		t.Fatal("archived_media_path column not added")
	}
}

func TestOpenRequiresPath(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Open(context.Background(), t.TempDir()); err == nil {
		t.Fatal("expected opening directory as db to fail")
	}
	if err := (*Store)(nil).Close(); err != nil {
		t.Fatal(err)
	}
	if unix(time.Time{}) != 0 {
		t.Fatal("zero time unix should be zero")
	}
	if !fromUnix(0).IsZero() {
		t.Fatal("zero unix should be zero time")
	}
}

func TestReplaceAllDuplicateSourcePKFails(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	err = st.ReplaceAll(ctx, ImportStats{FinishedAt: now}, nil,
		[]Chat{{JID: "chat", Kind: "dm", Name: "Chat", LastMessageAt: now}},
		nil,
		nil,
		[]Message{
			{SourcePK: 1, ChatJID: "chat", MessageID: "a", Timestamp: now, RawType: 0},
			{SourcePK: 1, ChatJID: "chat", MessageID: "b", Timestamp: now, RawType: 0},
		},
	)
	if err == nil {
		t.Fatal("expected duplicate source_pk error")
	}
	status, statusErr := st.Status(ctx)
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Messages != 0 {
		t.Fatalf("failed replace should roll back, got %+v", status)
	}
}
