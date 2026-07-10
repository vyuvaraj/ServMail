package queue

import (
	"os"
	"testing"
	"time"
)

func TestNewDiskQueueEmpty(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "queue-test-*")
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	q := NewDiskQueue(tmpFile.Name())
	if q.Size() != 0 {
		t.Errorf("expected empty queue, got size %d", q.Size())
	}
}

func TestDiskQueueEnqueue(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "queue-test-*")
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	q := NewDiskQueue(tmpFile.Name())
	email := &QueuedEmail{
		ID:      "id-1",
		Channel: "email",
		Target:  "test@example.com",
		Body:    "body",
		Status:  "pending",
	}
	q.Enqueue(email)

	if q.Size() != 1 {
		t.Errorf("expected size 1, got %d", q.Size())
	}
}

func TestDiskQueueMarkSent(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "queue-test-*")
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	q := NewDiskQueue(tmpFile.Name())
	email := &QueuedEmail{ID: "id-1", Status: "pending"}
	q.Enqueue(email)
	q.MarkSent("id-1")

	pending := q.PendingEntries()
	if len(pending) != 0 {
		t.Errorf("expected 0 pending, got %d", len(pending))
	}
}

func TestDiskQueueMarkFailed(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "queue-test-*")
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	q := NewDiskQueue(tmpFile.Name())
	email := &QueuedEmail{ID: "id-1", Status: "pending"}
	q.Enqueue(email)
	q.MarkFailed("id-1", "some error")

	pending := q.PendingEntries()
	if len(pending) != 0 {
		t.Errorf("expected failed item to not be pending, got %d", len(pending))
	}
	if q.entries[0].Attempts != 1 || q.entries[0].LastError != "some error" {
		t.Errorf("unexpected failed state: %+v", q.entries[0])
	}
}

func TestDiskQueuePendingEntries(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "queue-test-*")
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	q := NewDiskQueue(tmpFile.Name())
	q.Enqueue(&QueuedEmail{ID: "id-1", Status: "pending"})
	q.Enqueue(&QueuedEmail{ID: "id-2", Status: "sent"})

	pending := q.PendingEntries()
	if len(pending) != 1 || pending[0].ID != "id-1" {
		t.Errorf("expected only pending id-1, got %d items", len(pending))
	}
}

func TestDiskQueueSize(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "queue-test-*")
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	q := NewDiskQueue(tmpFile.Name())
	if q.Size() != 0 {
		t.Error("expected 0")
	}
	q.Enqueue(&QueuedEmail{ID: "1"})
	if q.Size() != 1 {
		t.Error("expected 1")
	}
}

func TestDiskQueueEnforceRetention(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "queue-test-*")
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	q := NewDiskQueue(tmpFile.Name())
	now := time.Now()
	// pending should not be purged
	q.Enqueue(&QueuedEmail{ID: "1", Status: "pending", QueuedAt: now.Add(-10 * time.Minute)})
	// sent and older than retention age should be purged
	q.Enqueue(&QueuedEmail{ID: "2", Status: "sent", QueuedAt: now.Add(-10 * time.Minute)})
	// sent but fresh should not be purged
	q.Enqueue(&QueuedEmail{ID: "3", Status: "sent", QueuedAt: now})

	q.EnforceRetention(5 * time.Minute)

	if q.Size() != 2 {
		t.Errorf("expected size 2 after retention purge, got %d", q.Size())
	}
}

func TestDiskQueueReload(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "queue-test-*")
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	q1 := NewDiskQueue(tmpFile.Name())
	q1.Enqueue(&QueuedEmail{ID: "reload-1", Status: "pending"})

	q2 := NewDiskQueue(tmpFile.Name())
	if q2.Size() != 1 || q2.entries[0].ID != "reload-1" {
		t.Errorf("failed to reload persisted queue entries")
	}
}

func TestDiskQueueCorruptJSONL(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "queue-test-*")
	defer os.Remove(tmpFile.Name())
	// write invalid data
	os.WriteFile(tmpFile.Name(), []byte("invalid json\n"), 0600)
	tmpFile.Close()

	q := NewDiskQueue(tmpFile.Name())
	if q.Size() != 0 {
		t.Errorf("expected corrupt entry to be skipped, got size %d", q.Size())
	}
}

func TestDiskQueueSplitLines(t *testing.T) {
	data := []byte("line1\nline2")
	lines := splitLines(data)
	if len(lines) != 2 || string(lines[0]) != "line1" || string(lines[1]) != "line2" {
		t.Errorf("splitLines failed: %v", lines)
	}
}

func TestDiskQueueMarkAbsent(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "queue-test-*")
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	q := NewDiskQueue(tmpFile.Name())
	q.Enqueue(&QueuedEmail{ID: "1", Status: "pending"})
	q.MarkSent("non-existent")
	if q.entries[0].Status != "pending" {
		t.Error("expected first entry status to remain unchanged")
	}
}
