package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/daemon/logger"
)

const composeTag = `{{$service := index .ContainerLabels "com.docker.compose.service"}}{{$number := index .ContainerLabels "com.docker.compose.container-number"}}{{if and $service $number}}{{$service}}-{{$number}}{{else}}{{.Name}}{{end}}`

func TestResolveAppName(t *testing.T) {
	const original = "matching-omnimatching-dataplane-1"
	tests := []struct {
		name, tag, containerName, id, service, number, want string
	}{
		{name: "no tag", containerName: "/" + original, want: original},
		{name: "empty tag", tag: "", containerName: "/" + original, want: original},
		{name: "dataplane one", tag: composeTag, service: "dataplane", number: "1", want: "dataplane-1"},
		{name: "dataplane two", tag: composeTag, service: "dataplane", number: "2", want: "dataplane-2"},
		{name: "hyphenated service", tag: composeTag, service: "matching-queue", number: "1", want: "matching-queue-1"},
		{name: "missing service", tag: composeTag, containerName: "/" + original, number: "1", want: original},
		{name: "missing number", tag: composeTag, containerName: "/" + original, service: "dataplane", want: original},
		{name: "empty rendered name", tag: `{{""}}`, containerName: "/" + original, want: original},
		{name: "empty rendered ID fallback", tag: `{{""}}`, id: "123456789012abcdef", want: "123456789012"},
		{name: "default ID fallback", id: "123456789012abcdef", want: "123456789012"},
		{name: "short ID fallback", tag: `{{""}}`, id: "abc", want: "abc"},
		{name: "short default ID fallback", id: "abc", want: "abc"},
		{name: "empty ID", tag: `{{""}}`, want: ""},
		{name: "Docker info field", tag: `{{.ContainerImageName}}`, want: "test-image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := map[string]string{}
			if tt.service != "" {
				labels["com.docker.compose.service"] = tt.service
			}
			if tt.number != "" {
				labels["com.docker.compose.container-number"] = tt.number
			}
			got, err := resolveAppName(logger.Info{
				Config: map[string]string{"tag": tt.tag}, ContainerName: tt.containerName,
				ContainerID: tt.id, ContainerLabels: labels, ContainerImageName: "test-image",
			})
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("app name = %q, want %q", got, tt.want)
			}
		})
	}
	// Docker's default also works with no config or label map.
	got, err := resolveAppName(logger.Info{ContainerName: "/" + original})
	if err != nil || got != original {
		t.Fatalf("nil maps: app name = %q, err = %v", got, err)
	}
	got, err = resolveAppName(logger.Info{Config: map[string]string{"tag": composeTag}, ContainerName: "/" + original})
	if err != nil || got != original {
		t.Fatalf("nil labels: app name = %q, err = %v", got, err)
	}
}

func TestNewSwoLogShipperRejectsInvalidTag(t *testing.T) {
	tests := []struct{ name, tag, wantError string }{
		{"malformed", "{{", "failed to resolve log tag"},
		{"execution failure", "{{.MissingField}}", "failed to resolve log tag"},
		{"space", "bad name", "whitespace and control characters"},
		{"tab", "bad\tname", "whitespace and control characters"},
		{"newline", "bad\nname", "whitespace and control characters"},
		{"null", "bad\x00name", "whitespace and control characters"},
		{"delete", "bad\x7fname", "whitespace and control characters"},
		{"unicode space", "bad\u00a0name", "whitespace and control characters"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shipper, err := newSwoLogShipper(logger.Info{Config: map[string]string{
				"swo-url": "http://unused.invalid", "swo-token": "test-token", "tag": tt.tag,
			}, ContainerName: "/original"})
			if shipper != nil {
				shipper.Close()
				t.Fatal("expected no shipper on startup error")
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want containing %q", err, tt.wantError)
			}
		})
	}
}

func TestShipperHTTPTagAndHostname(t *testing.T) {
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	for _, serviceName := range []string{"", "explicit-service"} {
		t.Run("service="+serviceName, func(t *testing.T) {
			type request struct {
				body, serviceHeader, auth, contentType string
				err                                    error
			}
			received := make(chan request, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				received <- request{string(body), r.Header.Get("X-Otel-Resource-Attr"), r.Header.Get("Authorization"), r.Header.Get("Content-Type"), err}
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()
			info := logger.Info{
				Config:          map[string]string{"swo-url": server.URL, "swo-token": "test-token", "tag": composeTag, "swo-service-name": serviceName},
				ContainerName:   "/matching-omnimatching-dataplane-1",
				ContainerLabels: map[string]string{"com.docker.compose.service": "dataplane", "com.docker.compose.container-number": "1"},
			}
			shipper, err := newSwoLogShipper(info)
			if err != nil {
				t.Fatal(err)
			}
			defer shipper.Close()
			if shipper.hostname != hostname {
				t.Fatalf("hostname = %q, want %q", shipper.hostname, hostname)
			}
			// Mutations after startup must not change the resolved app name.
			info.Config["tag"] = "changed"
			info.ContainerLabels["com.docker.compose.service"] = "changed"
			timestamp := time.Date(2026, 10, 7, 12, 0, 0, 123, time.UTC)
			if err := shipper.Log(&logger.Message{Timestamp: timestamp, Line: []byte(`{ "level": "error", "message": "test" }`)}); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-received:
				if got.err != nil {
					t.Fatal(got.err)
				}
				want := fmt.Sprintf("<11>1 %s %s dataplane-1 - - - %s", timestamp.Format(time.RFC3339Nano), hostname, `{"level":"error","message":"test"}`)
				if got.body != want {
					t.Fatalf("syslog = %q, want %q", got.body, want)
				}
				wantHeader := ""
				if serviceName != "" {
					wantHeader = "service.name=" + serviceName
				}
				if got.serviceHeader != wantHeader {
					t.Fatalf("service header = %q, want %q", got.serviceHeader, wantHeader)
				}
				if got.auth != "Bearer test-token" || got.contentType != "application/octet-stream" {
					t.Fatalf("unexpected HTTP headers: %+v", got)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("timed out waiting for shipped log")
			}
		})
	}
}
