package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

type severityConfig struct {
	paths  []string
	levels map[string]int
}

func newSeverityConfig(config map[string]string) (severityConfig, error) {
	result := severityConfig{paths: []string{"level", "LogLevel"}}
	if value, present := config["swo-level-paths"]; present {
		result.paths = nil
		if err := json.Unmarshal([]byte(value), &result.paths); err != nil || len(result.paths) == 0 {
			return result, fmt.Errorf("swo-level-paths must be a nonempty JSON array of GJSON paths")
		}
		for _, path := range result.paths {
			if strings.TrimSpace(path) == "" {
				return result, fmt.Errorf("swo-level-paths must not contain empty paths")
			}
		}
	}
	if value, present := config["swo-level-map"]; present {
		var levels map[string]*int
		if err := json.Unmarshal([]byte(value), &levels); err != nil || levels == nil {
			return result, fmt.Errorf("swo-level-map must be a JSON object mapping values to syslog severities 0 through 7")
		}
		result.levels = make(map[string]int, len(levels))
		for key, severity := range levels {
			key = strings.ToLower(strings.TrimSpace(key))
			if key == "" || severity == nil || *severity < 0 || *severity > 7 {
				return result, fmt.Errorf("swo-level-map requires nonempty keys and integer severities 0 through 7")
			}
			if _, exists := result.levels[key]; exists {
				return result, fmt.Errorf("swo-level-map has duplicate case-insensitive keys")
			}
			result.levels[key] = *severity
		}
	}
	return result, nil
}

func (config severityConfig) severity(message string) int {
	if gjson.Valid(message) {
		for _, path := range config.paths {
			value := gjson.Get(message, path)
			if value.Type != gjson.String && value.Type != gjson.Number {
				continue
			}
			level := strings.ToLower(strings.TrimSpace(value.String()))
			if severity, exists := config.levels[level]; exists {
				return severity
			}
			if severity, recognized := namedSeverity(level); recognized {
				return severity
			}
		}
		return 6
	}
	if strings.Contains(strings.ToLower(message), "error") {
		return 3
	}
	return 6
}

func namedSeverity(level string) (int, bool) {
	switch {
	case strings.HasPrefix(level, "emerg"):
		return 0, true
	case level == "alert":
		return 1, true
	case strings.HasPrefix(level, "crit"):
		return 2, true
	case level == "error":
		return 3, true
	case strings.HasPrefix(level, "warn"):
		return 4, true
	case level == "notice":
		return 5, true
	case strings.HasPrefix(level, "info"):
		return 6, true
	case level == "debug", level == "trace":
		return 7, true
	default:
		return 0, false
	}
}
