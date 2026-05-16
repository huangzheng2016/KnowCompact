package compact

import (
	"fmt"
	"io"
	stdlog "log"
	"os"
	"strings"
	"sync"
)

// LogLevel 日志级别.
//
// 零值（LogLevelUnset）会被 getLogger 解释为 LogLevelInfo —— 这样用户在
// CompactionConfig 中不显式设置 LogLevel 时仍能拿到合理默认行为，
// 同时不破坏 LogLevelDebug 的可达性。
type LogLevel int

const (
	// LogLevelUnset 零值，等同于 LogLevelInfo.
	LogLevelUnset LogLevel = iota
	// LogLevelDebug 详细诊断信息，仅用于排查问题.
	LogLevelDebug
	// LogLevelInfo 常规事件，如压缩触发、阈值穿越.
	LogLevelInfo
	// LogLevelWarn 异常但可恢复的情况，如断路器即将触发.
	LogLevelWarn
	// LogLevelError 操作失败，需要外部关注.
	LogLevelError
	// LogLevelSilent 静默，关闭所有输出.
	LogLevelSilent
)

// String 返回 LogLevel 的可读名称.
func (l LogLevel) String() string {
	switch l {
	case LogLevelUnset:
		return "UNSET"
	case LogLevelDebug:
		return "DEBUG"
	case LogLevelInfo:
		return "INFO"
	case LogLevelWarn:
		return "WARN"
	case LogLevelError:
		return "ERROR"
	case LogLevelSilent:
		return "SILENT"
	default:
		return fmt.Sprintf("LEVEL(%d)", int(l))
	}
}

// effectiveLevel 将 LogLevelUnset 解释为 LogLevelInfo.
func (l LogLevel) effectiveLevel() LogLevel {
	if l == LogLevelUnset {
		return LogLevelInfo
	}
	return l
}

// ParseLogLevel 从字符串解析日志级别（大小写不敏感）.
// 未识别时返回 LogLevelInfo.
func ParseLogLevel(s string) LogLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LogLevelDebug
	case "info":
		return LogLevelInfo
	case "warn", "warning":
		return LogLevelWarn
	case "error", "err":
		return LogLevelError
	case "silent", "off", "none":
		return LogLevelSilent
	default:
		return LogLevelInfo
	}
}

// Logger 结构化日志接口。
//
// 设计目标:
//   - 不绑定到具体日志库（slog/zap/logrus 均可适配）
//   - args 采用 (key, value, key, value, ...) 形式，与 slog 兼容
//   - 用户传入自定义实现即可对接生产日志系统
//
// 实现示例（slog 适配）:
//
//	type SlogAdapter struct{ Logger *slog.Logger }
//	func (s SlogAdapter) Debug(msg string, args ...any) { s.Logger.Debug(msg, args...) }
//	func (s SlogAdapter) Info(msg string, args ...any)  { s.Logger.Info(msg, args...) }
//	func (s SlogAdapter) Warn(msg string, args ...any)  { s.Logger.Warn(msg, args...) }
//	func (s SlogAdapter) Error(msg string, args ...any) { s.Logger.Error(msg, args...) }
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// stdLogger 默认实现，封装 stdlib log 包并按 level 过滤.
type stdLogger struct {
	mu    sync.Mutex
	level LogLevel
	out   *stdlog.Logger
}

// NewStdLogger 创建基于标准库 log 包的 Logger。
// w 为 nil 时使用 os.Stderr.
func NewStdLogger(level LogLevel, w io.Writer) Logger {
	if w == nil {
		w = os.Stderr
	}
	return &stdLogger{
		level: level,
		out:   stdlog.New(w, "", stdlog.LstdFlags|stdlog.Lmicroseconds),
	}
}

// NewDefaultLogger 返回默认 logger（INFO 级别，stderr 输出）.
func NewDefaultLogger() Logger {
	return NewStdLogger(LogLevelInfo, nil)
}

// NewNopLogger 返回静默 logger（不输出任何信息）.
func NewNopLogger() Logger { return nopLogger{} }

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}

func (s *stdLogger) Debug(msg string, args ...any) { s.log(LogLevelDebug, msg, args) }
func (s *stdLogger) Info(msg string, args ...any)  { s.log(LogLevelInfo, msg, args) }
func (s *stdLogger) Warn(msg string, args ...any)  { s.log(LogLevelWarn, msg, args) }
func (s *stdLogger) Error(msg string, args ...any) { s.log(LogLevelError, msg, args) }

func (s *stdLogger) log(level LogLevel, msg string, args []any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if level < s.level.effectiveLevel() {
		return
	}
	s.out.Print(formatLogLine(level, msg, args))
}

// formatLogLine 将 (key, value, ...) 形式的 args 格式化为可读串.
//
//	"[compact][INFO] auto-compact triggered tokens_before=12345 threshold=10000"
func formatLogLine(level LogLevel, msg string, args []any) string {
	var b strings.Builder
	b.WriteString("[compact][")
	b.WriteString(level.String())
	b.WriteString("] ")
	b.WriteString(msg)
	for i := 0; i < len(args); i += 2 {
		key := fmt.Sprint(args[i])
		var val any = "<MISSING>"
		if i+1 < len(args) {
			val = args[i+1]
		}
		b.WriteString(" ")
		b.WriteString(key)
		b.WriteString("=")
		fmt.Fprintf(&b, "%v", val)
	}
	return b.String()
}

// getLogger 从 config 中取 logger，未设置时使用默认 logger（受 LogLevel 控制）.
//
// 该函数在所有需要日志输出的内部代码中调用，避免到处写 nil 判断。
// LogLevelUnset 自动解释为 LogLevelInfo（详见 LogLevel 文档）。
func getLogger(config CompactionConfig) Logger {
	if config.Logger != nil {
		return config.Logger
	}
	return NewStdLogger(config.LogLevel, nil)
}

// GetLogger 是 getLogger 的导出版本，供 eino 适配器等子包使用.
//
// 不保证线程安全 —— 与底层 stdLogger 一致，每次调用产生新实例。
// 如需复用同一实例，请在 CompactionConfig.Logger 中显式注入。
func GetLogger(config CompactionConfig) Logger {
	return getLogger(config)
}
