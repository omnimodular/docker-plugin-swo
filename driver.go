package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/api/types/plugins/logdriver"
	"github.com/docker/docker/daemon/logger"
	protoio "github.com/gogo/protobuf/io"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/containerd/fifo"
)

type driver struct {
	mu   sync.Mutex
	logs map[string]*logPair
	idx  map[string]*logPair
}

type logPair struct {
	logShipper logger.Logger
	logReader  logger.LogReader
	stream     io.ReadCloser
	info       logger.Info
	done       chan struct{}
}

func newDriver() *driver {
	return &driver{
		logs: make(map[string]*logPair),
		idx:  make(map[string]*logPair),
	}
}

func (d *driver) StartLogging(file string, logCtx logger.Info) error {
	logrus.Info("SWO - Start logging")

	d.mu.Lock()
	if _, exists := d.logs[file]; exists {
		d.mu.Unlock()
		return fmt.Errorf("logger for %q already exists", file)
	}
	d.mu.Unlock()

	if logCtx.LogPath == "" {
		logCtx.LogPath = filepath.Join("/var/log/docker", logCtx.ContainerID)
	}
	if err := os.MkdirAll(filepath.Dir(logCtx.LogPath), 0755); err != nil {
		return errors.Wrap(err, "error setting up logger dir")
	}

	l, err := newSwoLogShipper(logCtx)
	if err != nil {
		return errors.Wrap(err, "error creating SWO log shipper")
	}

	r, err := newSwoLogReader(logCtx)
	if err != nil {
		return errors.Wrap(err, "error creating SWO log reader")
	}

	logrus.WithField("id", logCtx.ContainerID).WithField("file", file).WithField("logpath", logCtx.LogPath).Debug("Start logging")
	f, err := fifo.OpenFifo(context.Background(), file, syscall.O_RDONLY, 0700)
	if err != nil {
		return errors.Wrapf(err, "error opening logger fifo: %q", file)
	}

	d.mu.Lock()
	lf := &logPair{
		logShipper: l,
		logReader:  r,
		stream:     f,
		info:       logCtx,
		done:       make(chan struct{}),
	}
	d.logs[file] = lf
	d.idx[logCtx.ContainerID] = lf
	d.mu.Unlock()

	go d.consumeLog(lf)
	return nil
}

func (d *driver) StopLogging(file string) error {
	logrus.WithField("file", file).Debug("Stop logging")
	d.mu.Lock()
	lf, ok := d.logs[file]
	if ok {
		close(lf.done)
		lf.logShipper.Close()
		lf.stream.Close()
		delete(d.logs, file)
	}
	d.mu.Unlock()
	return nil
}

func (d *driver) consumeLog(lf *logPair) {
	dec := protoio.NewUint32DelimitedReader(lf.stream, binary.BigEndian, 1e6)
	defer dec.Close()
	var buf logdriver.LogEntry
	for {
		select {
		case <-lf.done:
			return
		default:
		}

		if err := dec.ReadMsg(&buf); err != nil {
			if err == io.EOF {
				logrus.WithField("id", lf.info.ContainerID).WithError(err).Debug("shutting down log logger")
				lf.stream.Close()
				return
			}
			dec = protoio.NewUint32DelimitedReader(lf.stream, binary.BigEndian, 1e6)
		}
		var msg logger.Message
		msg.Line = buf.Line
		msg.Source = buf.Source
		msg.Timestamp = time.Unix(0, buf.TimeNano)

		if err := lf.logShipper.Log(&msg); err != nil {
			logrus.WithField("id", lf.info.ContainerID).WithError(err).WithField("message", msg).Error("error writing log message")
			continue
		}

		buf.Reset()
	}
}

func (d *driver) ReadLogs(info logger.Info, config logger.ReadConfig) (io.ReadCloser, error) {
	d.mu.Lock()
	lf, exists := d.idx[info.ContainerID]
	d.mu.Unlock()
	if !exists {
		return nil, fmt.Errorf("SWO logger does not exist for %s", info.ContainerID)
	}

	r, w := io.Pipe()
	lr, ok := lf.logReader.(logger.LogReader)
	if !ok {
		return nil, fmt.Errorf("SWO logger does not support reading")
	}

	go func() {
		watcher := lr.ReadLogs(config)

		enc := protoio.NewUint32DelimitedWriter(w, binary.BigEndian)
		defer enc.Close()
		defer watcher.ConsumerGone()

		var buf logdriver.LogEntry
		for {
			select {
			case <-lf.done:
				w.Close()
				return
			case msg, ok := <-watcher.Msg:
				if !ok {
					w.Close()
					return
				}

				buf.Line = msg.Line
				buf.TimeNano = msg.Timestamp.UnixNano()
				buf.Source = msg.Source

				if err := enc.WriteMsg(&buf); err != nil {
					w.CloseWithError(err)
					return
				}
			case err := <-watcher.Err:
				w.CloseWithError(err)
				return
			}

			buf.Reset()
		}
	}()

	return r, nil
}
