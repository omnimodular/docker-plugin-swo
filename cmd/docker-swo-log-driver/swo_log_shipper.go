package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/docker/docker/daemon/logger"
	"github.com/docker/docker/daemon/logger/loggerutils"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

const (
	swoDriverName = "SWO Log Driver"
	maxRetries    = 10
	retrySleep    = 1 * time.Second
	batchSize     = 10
	flushInterval = 1 * time.Second
	queueSize     = 1000
)

type swoLogShipper struct {
	url               string
	token             string
	serviceName       string
	hostname          string
	appName           string
	jsonLimit         int
	syslogLevelPrefix bool
	severityConfig    severityConfig
	httpClient        *http.Client

	queue chan string
	done  chan struct{}
	wg    sync.WaitGroup
}

func newSwoLogShipper(logCtx logger.Info) (*swoLogShipper, error) {
	url := logCtx.Config["swo-url"]
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("swo-url is required")
	}

	token := logCtx.Config["swo-token"]
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("swo-token is required")
	}

	serviceName := logCtx.Config["swo-service-name"]
	severityConfig, err := newSeverityConfig(logCtx.Config)
	if err != nil {
		return nil, err
	}

	jsonLimit := 20
	if limitStr := logCtx.Config["swo-json-limit"]; limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil {
			jsonLimit = v
		}
	}

	syslogLevelPrefix := true
	if v := logCtx.Config["swo-syslog-level-prefix"]; v == "false" || v == "0" {
		syslogLevelPrefix = false
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	appName, err := resolveAppName(logCtx)
	if err != nil {
		return nil, err
	}

	log.Infof("Creating SWO log shipper for %s (container: %s)", url, appName)

	s := &swoLogShipper{
		url:               url,
		token:             token,
		serviceName:       serviceName,
		hostname:          hostname,
		appName:           appName,
		jsonLimit:         jsonLimit,
		syslogLevelPrefix: syslogLevelPrefix,
		severityConfig:    severityConfig,
		httpClient:        &http.Client{Timeout: 30 * time.Second},
		queue:             make(chan string, queueSize),
		done:              make(chan struct{}),
	}

	s.wg.Add(1)
	go s.flushLoop()

	return s, nil
}

// resolveAppName evaluates Docker's tag once, keeping the historical name/ID fallback.
func resolveAppName(logCtx logger.Info) (string, error) {
	appName, err := loggerutils.ParseLogTag(logCtx, "{{.Name}}")
	if err != nil {
		return "", errors.Wrap(err, "failed to resolve log tag for syslog app name")
	}
	if appName == "" {
		appName = strings.TrimPrefix(logCtx.ContainerName, "/")
		if appName == "" {
			appName = logCtx.ContainerID
			if len(appName) > 12 {
				appName = appName[:12]
			}
		}
	}
	if strings.ContainsFunc(appName, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return "", fmt.Errorf("invalid log tag for syslog app name %q: whitespace and control characters are not allowed", appName)
	}
	return appName, nil
}

func (s *swoLogShipper) Name() string {
	return swoDriverName
}

func (s *swoLogShipper) Log(msg *logger.Message) error {
	if len(msg.Line) == 0 {
		return nil
	}

	line := string(msg.Line)
	var severity int
	if s.syslogLevelPrefix {
		if sev, stripped, ok := parseSyslogLevelPrefix(line); ok {
			severity = sev
			line = minifyJSON(stripped, s.jsonLimit)
		} else {
			severity = s.severityConfig.severity(line)
			line = minifyJSON(line, s.jsonLimit)
		}
	} else {
		severity = s.severityConfig.severity(line)
		line = minifyJSON(line, s.jsonLimit)
	}
	prival := 8 + severity

	syslogMsg := fmt.Sprintf("<%d>1 %s %s %s - - - %s",
		prival,
		msg.Timestamp.UTC().Format(time.RFC3339Nano),
		s.hostname,
		s.appName,
		line,
	)

	select {
	case s.queue <- syslogMsg:
	default:
		log.Warn("SWO log queue full, dropping message")
	}

	return nil
}

func (s *swoLogShipper) Close() error {
	close(s.done)
	s.wg.Wait()
	return nil
}

func (s *swoLogShipper) flushLoop() {
	defer s.wg.Done()

	batch := make([]string, 0, batchSize)
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		select {
		case msg, ok := <-s.queue:
			if !ok {
				s.sendBatch(batch)
				return
			}
			batch = append(batch, msg)
			if len(batch) >= batchSize {
				s.sendBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				s.sendBatch(batch)
				batch = batch[:0]
			}
		case <-s.done:
			// Drain remaining messages
			for {
				select {
				case msg := <-s.queue:
					batch = append(batch, msg)
				default:
					s.sendBatch(batch)
					return
				}
			}
		}
	}
}

func (s *swoLogShipper) sendBatch(batch []string) {
	for _, msg := range batch {
		s.sendWithRetry(msg)
	}
}

func (s *swoLogShipper) sendWithRetry(msg string) {
	for attempt := 0; attempt < maxRetries; attempt++ {
		err := s.send(msg)
		if err == nil {
			return
		}
		log.WithError(err).WithField("attempt", attempt+1).Warn("SWO send failed, retrying")
		time.Sleep(retrySleep)
	}
	log.Error("SWO send failed after max retries, dropping message")
}

func (s *swoLogShipper) send(msg string) error {
	req, err := http.NewRequest("POST", s.url, bytes.NewBufferString(msg))
	if err != nil {
		return errors.Wrap(err, "failed to create request")
	}

	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	if s.serviceName != "" {
		req.Header.Set("X-Otel-Resource-Attr", "service.name="+s.serviceName)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "failed to send log")
	}
	resp.Body.Close()

	if resp.StatusCode >= 500 {
		return fmt.Errorf("server error: %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		log.WithField("status", resp.StatusCode).Error("SWO rejected log (client error, not retrying)")
	}

	return nil
}

// parseSyslogLevelPrefix checks if msg starts with a systemd-style syslog level
// prefix of the form <N> where N is 0–7 (per sd-daemon(3) / SyslogLevelPrefix=).
// Returns (severity, stripped message, true) on match, otherwise (0, msg, false).
func parseSyslogLevelPrefix(msg string) (int, string, bool) {
	if len(msg) < 3 || msg[0] != '<' || msg[2] != '>' {
		return 0, msg, false
	}
	d := msg[1]
	if d < '0' || d > '7' {
		return 0, msg, false
	}
	return int(d - '0'), msg[3:], true
}

// recursiveReduce truncates large JSON structures while preserving shape.
// Keeps the first N/2 and last N/2 items with "..." in between.
// Matches the Python log-sidecar's recursive_reduce.
func recursiveReduce(obj interface{}, limit int) interface{} {
	if limit <= 0 {
		return obj
	}

	switch v := obj.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		if len(keys) > limit {
			left := (limit + 1) / 2 // ceiling division
			right := limit / 2
			kept := make([]string, 0, left+1+right)
			kept = append(kept, keys[:left]...)
			kept = append(kept, "...")
			kept = append(kept, keys[len(keys)-right:]...)
			keys = kept
		}

		result := make(map[string]interface{}, len(keys))
		for _, k := range keys {
			if k == "..." {
				result["..."] = "..."
			} else {
				result[k] = recursiveReduce(v[k], limit)
			}
		}
		return result

	case []interface{}:
		if len(v) > limit {
			left := (limit + 1) / 2
			right := limit / 2
			kept := make([]interface{}, 0, left+1+right)
			kept = append(kept, v[:left]...)
			kept = append(kept, "...")
			kept = append(kept, v[len(v)-right:]...)
			for i, item := range kept {
				kept[i] = recursiveReduce(item, limit)
			}
			return kept
		}
		for i, item := range v {
			v[i] = recursiveReduce(item, limit)
		}
		return v

	default:
		return obj
	}
}

// minifyJSON parses a string as JSON, applies recursiveReduce, and re-serializes.
// Returns the original string if it's not valid JSON or limit <= 0.
func minifyJSON(msg string, limit int) string {
	if limit <= 0 {
		return msg
	}
	var obj interface{}
	if err := json.Unmarshal([]byte(msg), &obj); err != nil {
		return msg
	}
	obj = recursiveReduce(obj, limit)
	out, err := json.Marshal(obj)
	if err != nil {
		return msg
	}
	return string(out)
}
