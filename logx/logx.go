package logx

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	phuslog "github.com/phuslu/log"
)

type LoggingConfig struct {
	Level            string            `mapstructure:"level"`
	Dir              string            `mapstructure:"dir"`
	Filename         string            `mapstructure:"filename"`
	MaxSizeMB        int64             `mapstructure:"max_size_mb"`
	MaxBackups       int               `mapstructure:"max_backups"`
	LocalTime        bool              `mapstructure:"local_time"`
	TimeFormat       string            `mapstructure:"time_format"`
	AsyncChannelSize uint              `mapstructure:"async_channel_size"`
	DiscardOnFull    bool              `mapstructure:"discard_on_full"`
	EnableCaller     bool              `mapstructure:"enable_caller"`
	ModuleFiles      map[string]string `mapstructure:"module_files"`
}

func DefaultConfig() LoggingConfig {
	return LoggingConfig{
		Level:            "info",
		Dir:              "./logs",
		Filename:         "polypilot.log",
		MaxSizeMB:        256,
		MaxBackups:       14,
		LocalTime:        false,
		TimeFormat:       "20060102",
		AsyncChannelSize: 16384,
		DiscardOnFull:    false,
		EnableCaller:     false,
		ModuleFiles:      nil,
	}
}

type moduleLogger struct {
	name string
	log  phuslog.Logger
}

type safeAsyncWriter struct {
	inner  *phuslog.AsyncWriter
	closed atomic.Bool
}

// moduleWriter tracks a per-module file writer for rotation and cleanup.
type moduleWriter struct {
	fileWriter *phuslog.FileWriter
	async      *safeAsyncWriter
	logger     phuslog.Logger
}

var (
	defaultLogger phuslog.Logger
	defaultWriter *phuslog.FileWriter
	closer        *safeAsyncWriter
	moduleCache   sync.Map

	// per-module writers, for daily rotation and shutdown
	moduleWriters   = make(map[string]*moduleWriter)
	moduleWritersMu sync.Mutex
	moduleFilesCfg  map[string]string

	// cached init options for creating per-module writers on demand
	initOpts LoggingConfig
	inited   bool
)

// buildLogger creates a FileWriter + AsyncWriter + Logger trio from the given
// directory, filename, and shared options.
func buildLogger(dir, filename string, opt LoggingConfig) (*phuslog.FileWriter, *safeAsyncWriter, phuslog.Logger) {
	level := parseLevel(opt.Level)
	caller := boolToInt(opt.EnableCaller)

	fw := &phuslog.FileWriter{
		Filename:     filepath.Join(dir, filename),
		MaxSize:      opt.MaxSizeMB * 1024 * 1024,
		MaxBackups:   opt.MaxBackups,
		LocalTime:    opt.LocalTime,
		TimeFormat:   opt.TimeFormat,
		EnsureFolder: true,
	}
	aw := &phuslog.AsyncWriter{
		Writer:        fw,
		ChannelSize:   opt.AsyncChannelSize,
		DiscardOnFull: opt.DiscardOnFull,
	}
	safeWriter := &safeAsyncWriter{inner: aw}
	logger := phuslog.Logger{
		Level:  level,
		Caller: caller,
		Writer: safeWriter,
	}
	return fw, safeWriter, logger
}

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

	fw, safeWriter, logger := buildLogger(opt.Dir, opt.Filename, opt)

	defaultLogger = logger
	defaultWriter = fw
	closer = safeWriter
	moduleCache = sync.Map{}

	// store config for creating per-module writers on demand
	initOpts = opt
	moduleFilesCfg = opt.ModuleFiles
	inited = true

	// upgrade any module loggers that were cached before Init (e.g. package-level
	// var declarations) and now have a dedicated file configured
	if len(opt.ModuleFiles) > 0 {
		moduleCache.Range(func(k, v any) bool {
			name := k.(string)
			if filename, ok := opt.ModuleFiles[name]; ok {
				ml := v.(*moduleLogger)
				ml.log = getOrCreateModuleLogger(name, filename)
			}
			return true
		})
	}

	return nil
}

func Bootstrap(ctx context.Context, opt LoggingConfig, loc *time.Location) (shutdown func() error, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err = Init(opt); err != nil {
		return nil, err
	}
	StartDailyRotate(ctx, loc)
	return Close, nil
}

func Close() error {
	moduleWritersMu.Lock()
	for _, mw := range moduleWriters {
		_ = mw.async.Close()
	}
	moduleWritersMu.Unlock()

	if closer != nil {
		return closer.Close()
	}
	return nil
}

func (w *safeAsyncWriter) WriteEntry(e *phuslog.Entry) (n int, err error) {
	if w == nil || w.closed.Load() {
		return 0, nil
	}
	defer func() {
		if recover() != nil {
			n, err = 0, nil
		}
	}()
	return w.inner.WriteEntry(e)
}

func (w *safeAsyncWriter) Close() error {
	if w == nil {
		return nil
	}
	if !w.closed.CompareAndSwap(false, true) {
		return nil
	}
	return w.inner.Close()
}

// getOrCreateModuleLogger returns a logger that writes to a module-specific file.
// Caller must hold moduleWritersMu (or be in a single-writer context like Init).
func getOrCreateModuleLogger(name, filename string) phuslog.Logger {
	if mw, ok := moduleWriters[name]; ok {
		return mw.logger
	}

	fw, safeWriter, logger := buildLogger(initOpts.Dir, filename, initOpts)

	moduleWriters[name] = &moduleWriter{
		fileWriter: fw,
		async:      safeWriter,
		logger:     logger,
	}
	return logger
}

func StartDailyRotate(ctx context.Context, loc *time.Location) {
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
				if defaultWriter != nil {
					_ = defaultWriter.Rotate()
				}
				moduleWritersMu.Lock()
				for _, mw := range moduleWriters {
					_ = mw.fileWriter.Rotate()
				}
				moduleWritersMu.Unlock()
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

	log := defaultLogger
	if inited {
		if filename, ok := moduleFilesCfg[key]; ok {
			moduleWritersMu.Lock()
			log = getOrCreateModuleLogger(key, filename)
			moduleWritersMu.Unlock()
		}
	}

	m := &moduleLogger{name: key, log: log}
	actual, _ := moduleCache.LoadOrStore(key, m)
	return actual.(*moduleLogger)
}

func Trace() *phuslog.Entry { return defaultLogger.Trace() }
func Debug() *phuslog.Entry { return defaultLogger.Debug() }
func Info() *phuslog.Entry  { return defaultLogger.Info() }
func Warn() *phuslog.Entry  { return defaultLogger.Warn() }
func Error() *phuslog.Entry { return defaultLogger.Error() }

func (m *moduleLogger) Trace() *phuslog.Entry { return m.log.Trace().Str("module", m.name) }
func (m *moduleLogger) Debug() *phuslog.Entry { return m.log.Debug().Str("module", m.name) }
func (m *moduleLogger) Info() *phuslog.Entry  { return m.log.Info().Str("module", m.name) }
func (m *moduleLogger) Warn() *phuslog.Entry  { return m.log.Warn().Str("module", m.name) }
func (m *moduleLogger) Error() *phuslog.Entry { return m.log.Error().Str("module", m.name) }

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
