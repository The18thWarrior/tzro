package invocation

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRecord_PreservesOnlyMetadataAndDoesNotChangeExecution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	input := `{"conversationId":"conversation-1","modelName":"gemini-3.8-flash-low","invocationNum":0,"initialNumSteps":1,"prompt":"private-prompt","apiKey":"private-key","transcriptPath":"/private/transcript"}`
	var output bytes.Buffer
	if err := Record("PreInvocation", path, strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output.String()) != "{}" {
		t.Fatalf("observer changed hook output: %s", &output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") || strings.Contains(string(data), "transcript") {
		t.Fatalf("observer retained non-metadata content: %s", data)
	}
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Kind != "PreInvocation" || event.Conversation != "conversation-1" || event.Invocation == nil || *event.Invocation != 0 || event.TimestampNS <= 0 {
		t.Fatalf("missing invocation identity: %+v", event)
	}
}

func TestRecord_ConcurrentWritesRetainCompleteMetadataRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.ndjson")
	var group sync.WaitGroup
	for n := 0; n < 24; n++ {
		group.Add(1)
		go func(n int) {
			defer group.Done()
			data, _ := json.Marshal(map[string]any{"conversationId": "concurrent", "invocationNum": n, "initialNumSteps": n})
			var output bytes.Buffer
			if err := Record("PreInvocation", path, bytes.NewReader(data), &output); err != nil {
				t.Error(err)
			}
		}(n)
	}
	group.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 24 {
		t.Fatalf("missing records: %d", len(lines))
	}
	seen := map[int]bool{}
	for _, line := range lines {
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		seen[*event.Invocation] = true
	}
	if len(seen) != 24 {
		t.Fatal("lost concurrent invocation identities")
	}
}

func TestRecord_InvalidPayloadRemainsPassiveAndCannotBecomeValidEvidence(t *testing.T) {
	for _, input := range []string{`{"apiKey":"private-key"}`, `broken private-key`, `{"conversationId":"x","invocationNum":0,"initialNumSteps":1} {"apiKey":"private-key"}`} {
		path := filepath.Join(t.TempDir(), "events.ndjson")
		var output bytes.Buffer
		if err := Record("PreInvocation", path, strings.NewReader(input), &output); err == nil {
			t.Fatal("invalid input accepted")
		}
		if strings.TrimSpace(output.String()) != "{}" {
			t.Fatal("invalid input changed execution")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "private-key") {
			t.Fatal("invalid payload leaked")
		}
		var event Event
		_ = json.Unmarshal(data, &event)
		if event.Invalid == "" {
			t.Fatal("invalid evidence not labeled")
		}
	}
}
