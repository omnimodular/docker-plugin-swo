package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/daemon/logger"
)

func TestShippedSeverityUsesConfiguredPathsBeforeTruncation(t *testing.T) {
	tests := []struct {
		name, message, priority string
		options                 map[string]string
	}{
		{"dotnet", `{"LogLevel":"Warning","Message":"test"}`, "<12>1", nil},
		{"mongo", `{"s":"W","msg":"test"}`, "<12>1", map[string]string{"swo-level-paths": `["s"]`, "swo-level-map": `{"W":4}`}},
		{"encoded JSON", `{"LogLevel":"Information","Message":"{\"level\":\"error\"}"}`, "<11>1", map[string]string{"swo-level-paths": `["Message|@fromstr|level","LogLevel"]`}},
		{"truncated selector", `{"a":0,"event":{"status":"error"},"z":0}`, "<11>1", map[string]string{"swo-level-paths": `["event.status"]`, "swo-json-limit": "1"}},
		{"prefix wins", `<4>{"level":"error"}`, "<12>1", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				received <- string(body)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			opts := map[string]string{"swo-url": server.URL, "swo-token": "test-token"}
			for key, value := range tt.options {
				opts[key] = value
			}
			shipper, err := newSwoLogShipper(logger.Info{Config: opts, ContainerName: "/probe"})
			if err != nil {
				t.Fatal(err)
			}
			defer shipper.Close()
			if err := shipper.Log(&logger.Message{Timestamp: time.Now(), Line: []byte(tt.message)}); err != nil {
				t.Fatal(err)
			}
			select {
			case body := <-received:
				if !strings.HasPrefix(body, tt.priority+" ") {
					t.Fatalf("wrong syslog severity: %s", body)
				}
				if tt.name == "truncated selector" && strings.Contains(body, "status") {
					t.Fatalf("fixture did not exercise truncation: %s", body)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for shipped log")
			}
		})
	}
}
