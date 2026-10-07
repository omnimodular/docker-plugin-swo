package main

import (
	"testing"

	"github.com/docker/docker/daemon/logger"
)

func TestSyslogSeverity(t *testing.T) {
	config, err := newSeverityConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, message string
		want          int
	}{
		{"dotnet trace", `{"LogLevel":"Trace"}`, 7},
		{"dotnet debug", `{"LogLevel":"Debug"}`, 7},
		{"dotnet information", `{"LogLevel":"Information","Message":"ConsecutiveErrors=0"}`, 6},
		{"dotnet warning", `{"LogLevel":"Warning"}`, 4},
		{"dotnet error", `{"LogLevel":"Error"}`, 3},
		{"dotnet critical", `{"LogLevel":"Critical"}`, 2},
		{"application level", `{"level":"error"}`, 3},
		{"application precedence", `{"level":"warning","LogLevel":"Information"}`, 4},
		{"invalid application level falls back", `{"level":3,"LogLevel":"Error"}`, 3},
		{"invalid dotnet level", `{"LogLevel":3}`, 6},
		{"unknown dotnet level", `{"LogLevel":"Unknown"}`, 6},
		{"missing level", `{"Message":"error"}`, 6},
		{"plain error", "an error occurred", 3},
		{"plain info", "started", 6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := config.severity(tt.message); got != tt.want {
				t.Fatalf("syslogSeverity(%q) = %d, want %d", tt.message, got, tt.want)
			}
		})
	}
}

func TestConfiguredSeverityPaths(t *testing.T) {
	tests := []struct {
		name, paths, levels, message string
		want                         int
	}{
		{"nested object", `["event.status"]`, "", `{"event":{"status":"warning"}}`, 4},
		{"nested encoded JSON", `["Message|@fromstr|level","LogLevel"]`, "", `{"LogLevel":"Information","Message":"{\"level\":\"error\"}"}`, 3},
		{"encoded JSON warning", `["Message|@fromstr|level","LogLevel"]`, "", `{"LogLevel":"Information","Message":"{\"level\":\"warning\"}"}`, 4},
		{"plain message fallback", `["Message|@fromstr|level","LogLevel"]`, "", `{"LogLevel":"Warning","Message":"not JSON"}`, 4},
		{"missing path fallback", `["missing","LogLevel"]`, "", `{"LogLevel":"Error"}`, 3},
		{"null path fallback", `["level","LogLevel"]`, "", `{"level":null,"LogLevel":"Error"}`, 3},
		{"object path fallback", `["level","LogLevel"]`, "", `{"level":{},"LogLevel":"Warning"}`, 4},
		{"unknown level fallback", `["level","LogLevel"]`, "", `{"level":"unknown","LogLevel":"Error"}`, 3},
		{"ordered precedence", `["severity","level"]`, "", `{"severity":"warning","level":"error"}`, 4},
		{"escaped key", `["event\\.level"]`, "", `{"event.level":"error"}`, 3},
		{"array element", `["events.0.level"]`, "", `{"events":[{"level":"warning"}]}`, 4},
		{"mongo warning", `["s"]`, `{"F":2,"E":3,"W":4,"I":6,"D1":7}`, `{"s":"W"}`, 4},
		{"mongo fatal", `["s"]`, `{"F":2,"E":3,"W":4,"I":6,"D1":7}`, `{"s":"F"}`, 2},
		{"mongo debug", `["s"]`, `{"F":2,"E":3,"W":4,"I":6,"D1":7}`, `{"s":"D1"}`, 7},
		{"numeric status mapping", `["status"]`, `{"500":3}`, `{"status":500}`, 3},
		{"mapping is case insensitive", `["status"]`, `{"FAILED":3}`, `{"status":"failed"}`, 3},
		{"custom mapping overrides standard", `["level"]`, `{"notice":4}`, `{"level":"notice"}`, 4},
		{"no implicit numeric severity", `["status"]`, "", `{"status":3}`, 6},
		{"unmatched JSON stays info", `["status"]`, "", `{"message":"error"}`, 6},
		{"text fallback retained", `["status"]`, "", "an error occurred", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := map[string]string{"swo-level-paths": tt.paths}
			if tt.levels != "" {
				opts["swo-level-map"] = tt.levels
			}
			config, err := newSeverityConfig(opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := config.severity(tt.message); got != tt.want {
				t.Fatalf("severity = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestInvalidSeverityConfigurationFailsStartup(t *testing.T) {
	tests := []map[string]string{
		{"swo-level-paths": "level"},
		{"swo-level-paths": "[]"},
		{"swo-level-paths": "null"},
		{"swo-level-paths": "[null]"},
		{"swo-level-paths": "[123]"},
		{"swo-level-paths": "[\" \" ]"},
		{"swo-level-map": "null"},
		{"swo-level-map": "[]"},
		{"swo-level-map": "{\"W\":null}"},
		{"swo-level-map": "{\"W\":4.5}"},
		{"swo-level-map": "{\"W\":8}"},
		{"swo-level-map": "{\"W\":-1}"},
		{"swo-level-map": "{\"W\":\"warning\"}"},
		{"swo-level-map": "{\"\":4}"},
		{"swo-level-map": "{\"W\":4,\"w\":3}"},
	}
	for _, opts := range tests {
		opts["swo-url"] = "http://unused.invalid"
		opts["swo-token"] = "test-token"
		shipper, err := newSwoLogShipper(logger.Info{Config: opts})
		if shipper != nil {
			shipper.Close()
			t.Fatalf("accepted invalid configuration: %v", opts)
		}
		if err == nil {
			t.Fatalf("missing startup error for %v", opts)
		}
	}
}
