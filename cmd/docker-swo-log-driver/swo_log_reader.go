package main

import (
	"github.com/docker/docker/daemon/logger"
	log "github.com/sirupsen/logrus"
)

// swoLogReader is a stub — remote log reading is not supported in v1.
// docker logs will not work with this driver.
type swoLogReader struct{}

func newSwoLogReader(logCtx logger.Info) (*swoLogReader, error) {
	log.Debug("Creating SWO log reader (stub — docker logs not supported)")
	return &swoLogReader{}, nil
}

func (r *swoLogReader) ReadLogs(config logger.ReadConfig) *logger.LogWatcher {
	watcher := logger.NewLogWatcher()
	close(watcher.Msg)
	return watcher
}
