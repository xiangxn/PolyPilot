package logx

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"time"

	phuslog "github.com/phuslu/log"
)

type LoggingConfig struct {
	Level            string `mapstructure:"level"`
	Dir              string `mapstructure:"dir"`
	Filename         string `mapstructure:"filename"`
	MaxSizeMB        int64  `mapstructure:"max_size_mb"`
	MaxBackups       int    `mapstructure:"max_backups"`
	LocalTime        bool   `mapstructure:"local_time"`
	AsyncChannelSize uint   `mapstructure:"async_channel_size"`
	DiscardOnFull    bool   `mapstructure:"discard_on_full"`
	EnableCaller     bool   `mapstructure:"enable_caller"`
}

func DefaultConfig() LoggingConfig {
	return LoggingConfig{
		Level:            "info",
		Dir:              "./logs",
		Filename:         "polypilot.log",
		MaxSizeMB:        256,
		MaxBackups:       14,
		LocalTime:        true,
		AsyncChannelSize: 16384,
		DiscardOnFull:    false,
		EnableCaller:     false,
	}
}

type moduleLogger struct {
	name string
}

var (
	defaultLogger phuslog.Logger
	fileWriter    *phuslog.FileWriter
	closer        interface{ Close() error }
	moduleCache   sync.Map
)

func Init(opt LoggingConfig) error {
	if strings.TrimSpace(opt.Level) == "" {
		opt.Level = "info"
	}
	if strings.TrimSpace(opt.Dir) == "" {
		opt.Dir = "./logs"
	}
	if strings.TrimSpace(opt.Filename) == "" {
		opt.Filename = "polypilot.log"
	}
	if opt.MaxSizeMB <= 0 {
		opt.MaxSizeMB = 256
	}
	if opt.MaxBackups <= 0 {
		opt.MaxBackups = 14
	}
	if opt.AsyncChannelSize == 0 {
		opt.AsyncChannelSize = 16384
	}

	fw := &phuslog.FileWriter{
		Filename:     filepath.Join(opt.Dir, opt.Filename),
		MaxSize:      opt.MaxSizeMB * 1024 * 1024,
		MaxBackups:   opt.MaxBackups,
		LocalTime:    opt.LocalTime,
		EnsureFolder: true,
	}
	aw := &phuslog.AsyncWriter{
		Writer:        fw,
		ChannelSize:   opt.AsyncChannelSize,
		DiscardOnFull: opt.DiscardOnFull,
	}

	defaultLogger = phuslog.Logger{
		Level:  parseLevel(opt.Level),
		Caller: boolToInt(opt.EnableCaller),
		Writer: aw,
	}
	fileWriter = fw
	closer = aw
	moduleCache = sync.Map{}
	return nil
}

func Close() error {
	if closer != nil {
		return closer.Close()
	}
	return nil
}

func StartDailyRotate(ctx context.Context, loc *time.Location) {
	if fileWriter == nil {
		return
	}
	if loc == nil {
		loc = time.Local
	}
	go func() {
		for {
			now := time.Now().In(loc)
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, loc)
			t := time.NewTimer(time.Until(next))
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
				_ = fileWriter.Rotate()
			}
		}
	}()
}

func Module(name string) *moduleLogger {
	key := strings.TrimSpace(name)
	if key == "" {
		key = "default"
	}
	if v, ok := moduleCache.Load(key); ok {
		return v.(*moduleLogger)
	}
	m := &moduleLogger{name: key}
	actual, _ := moduleCache.LoadOrStore(key, m)
	return actual.(*moduleLogger)
}

func Trace() *phuslog.Entry { return defaultLogger.Trace() }
func Debug() *phuslog.Entry { return defaultLogger.Debug() }
func Info() *phuslog.Entry  { return defaultLogger.Info() }
func Warn() *phuslog.Entry  { return defaultLogger.Warn() }
func Error() *phuslog.Entry { return defaultLogger.Error() }

func (m *moduleLogger) Trace() *phuslog.Entry { return defaultLogger.Trace().Str("module", m.name) }
func (m *moduleLogger) Debug() *phuslog.Entry { return defaultLogger.Debug().Str("module", m.name) }
func (m *moduleLogger) Info() *phuslog.Entry  { return defaultLogger.Info().Str("module", m.name) }
func (m *moduleLogger) Warn() *phuslog.Entry  { return defaultLogger.Warn().Str("module", m.name) }
func (m *moduleLogger) Error() *phuslog.Entry { return defaultLogger.Error().Str("module", m.name) }

func parseLevel(level string) phuslog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "trace":
		return phuslog.TraceLevel
	case "debug":
		return phuslog.DebugLevel
	case "warn", "warning":
		return phuslog.WarnLevel
	case "error":
		return phuslog.ErrorLevel
	case "panic":
		return phuslog.PanicLevel
	case "fatal":
		return phuslog.FatalLevel
	default:
		return phuslog.InfoLevel
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
