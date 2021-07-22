package rzlog

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"time"

	"github.com/fsnotify/fsnotify"
	grpc_middleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/kyawmyintthein/rzerrors"
	"github.com/kyawmyintthein/rzmiddleware"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

type (
	LoggerCtxKey   struct{}
	LogEntryCtxKey struct{}
	KV             map[string]interface{}

	Logger interface {
		Error(context.Context, error, ...interface{})
		Warn(context.Context, ...interface{})
		Info(context.Context, ...interface{})
		Debug(context.Context, ...interface{})

		Errorf(context.Context, error, string, ...interface{})
		Warnf(context.Context, string, ...interface{})
		Infof(context.Context, string, ...interface{})
		Debugf(context.Context, string, ...interface{})

		ErrorKV(context.Context, error, KV, ...interface{})
		WarnKV(context.Context, KV, ...interface{})
		InfoKV(context.Context, KV, ...interface{})
		DebugKV(context.Context, KV, ...interface{})

		ErrorKVf(context.Context, error, KV, string, ...interface{})
		WarnKVf(context.Context, KV, string, ...interface{})
		InfoKVf(context.Context, KV, string, ...interface{})
		DebugKVf(context.Context, KV, string, ...interface{})

		SetLogLevel(string) error
		NewRequestLogger() func(next http.Handler) http.Handler
		UnaryServerInterceptor(...Option) grpc.UnaryServerInterceptor
		StreamServerInterceptor(...Option) grpc.StreamServerInterceptor
	}

	LogEntry interface {
		Write(status, bytes int, header http.Header, elapsed time.Duration, extra interface{})
		Panic(v interface{}, stack []byte)
	}

	logger struct {
		cfg     LogCfg
		logrus  *logrus.Logger
		logfile *os.File
		absPath string
	}
)

const (
	_jsonLogFormat   = "json"
	_textLogFormat   = "text"
	_defaultLogLevel = logrus.InfoLevel
	// SystemField is used in every log statement made through grpc_logrus. Can be overwritten before any initialization code.
	_systemField = "system"
	// KindField describes the log field used to indicate whether this is a server or a client log statement.
	_kindField      = "span.kind"
	_logEntryCtxKey = "LogEntryCtxKey"
)

var (
	_defaultFileFlag = os.O_APPEND | os.O_CREATE | os.O_WRONLY
	_defaultFileMode = os.FileMode(0755)
	_stdLogger       Logger
)

func (logEntryCtxKey LogEntryCtxKey) String() string {
	return _logEntryCtxKey

}
func Init(cfg LogCfg) Logger {
	if _stdLogger == nil {
		_stdLogger = new(cfg)
	}
	return _stdLogger
}

func New(cfg LogCfg) Logger {
	return new(cfg)
}

func new(cfg LogCfg) Logger {
	logger := &logger{
		cfg: cfg,
	}
	logger.newLogrus()

	go logger.watchLogRotation()

	return logger
}

// create new logrus object
func (logger *logger) newLogrus() {
	logger.logrus = &logrus.Logger{
		Hooks: make(logrus.LevelHooks),
	}

	logLevel, err := logrus.ParseLevel(logger.cfg.Level)
	if err != nil {
		logLevel = _defaultLogLevel
	}
	logger.logrus.Level = logLevel

	switch logger.cfg.Format {
	case _jsonLogFormat:
		logger.logrus.SetFormatter(&logrus.JSONFormatter{})
	default:
		logger.logrus.SetFormatter(&logrus.TextFormatter{})
	}

	logger.logrus.Out = os.Stdout
	if logger.cfg.FilePath != "" {
		logfile, err := os.OpenFile(logger.cfg.FilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0755)
		if err != nil {
			logger.logrus.Errorf("[%s]:: failed to set log file. Error : '%v'. set 'stdout' as default", PackageName, err)
			return
		} else {
			logger.logfile = logfile
			logger.logrus.Out = logger.logfile
		}

		logger.logrus.Errorf("[%s]:: empty log file. set 'Stdout' as default", PackageName)
	} else {
		logger.logrus.Errorf("[%s]:: empty log file. set 'Stdout' as default", PackageName)
	}

	logger.logrus.Infof("[%s]:: init successfully", PackageName)
}

func (logger *logger) watchLogRotation() {
	if !logger.cfg.Rotation {
		logger.Infof(context.Background(), "[%s]:: disabled log rotation", PackageName)
		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}

	// Process events
	go func() {
		for {
			select {
			case ev := <-watcher.Events:
				if ev.Name == logger.absPath && (ev.Op.String() == "REMOVE" || ev.Op.String() == "RENAME") {
					logger.logfile.Close()
					f, err := os.OpenFile(logger.cfg.FilePath, _defaultFileFlag, _defaultFileMode)
					if err != nil {
						panic(err)
					}

					logger.logrus.Out = f
					logger.logfile = f

					err = watcher.Add(logger.logfile.Name())
					if err != nil {
						panic(err)
					}
				}
			case err := <-watcher.Errors:
				logger.Errorf(context.Background(), err, "[%s]:: failed to watch log file", PackageName)
			}
		}
	}()

	err = watcher.Add(logger.cfg.FilePath)
	if err != nil {
		panic(err)
	}
}

func (logger *logger) SetLogLevel(level string) error {
	logLevel, err := logrus.ParseLevel(level)
	if err != nil {
		logLevel = logrus.InfoLevel
		return err
	}
	logger.logrus.Level = logLevel
	return nil
}

func getLogger() Logger {
	if _stdLogger == nil {
		return new(LogCfg{})
	}
	return _stdLogger
}

func (l *logger) Error(ctx context.Context, err error, args ...interface{}) {
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.WithFields(getErrorFields(err)).Errorln(args...)
		return
	}
	l.logrus.WithFields(getErrorFields(err)).Errorln(args...)
}

func (l *logger) Warn(ctx context.Context, args ...interface{}) {
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.Warnln(args...)
		return
	}
	l.logrus.Warnln(args...)
}

func (l *logger) Info(ctx context.Context, args ...interface{}) {
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.Infoln(args...)
		return
	}
	l.logrus.Infoln(args...)
}

func (l *logger) Debug(ctx context.Context, args ...interface{}) {
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.Debugln(args...)
		return
	}
	l.logrus.Debugln(args...)
}

func (l *logger) Errorf(ctx context.Context, err error, message string, args ...interface{}) {
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.WithFields(getErrorFields(err)).Errorf(message, args...)
		return
	}
	l.logrus.WithFields(getErrorFields(err)).Errorf(message, args...)
}

func (l *logger) Warnf(ctx context.Context, message string, args ...interface{}) {
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.Warnf(message, args...)
		return
	}
	l.logrus.Warnf(message, args...)
}

func (l *logger) Infof(ctx context.Context, message string, args ...interface{}) {
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.Infof(message, args...)
		return
	}
	l.logrus.Infof(message, args...)
}

func (l *logger) Debugf(ctx context.Context, message string, args ...interface{}) {
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.Debugf(message, args...)
		return
	}
	l.logrus.Debugf(message, args...)
}

func (l *logger) ErrorKV(ctx context.Context, err error, kv KV, args ...interface{}) {
	fields := convertKvsToLogrusFields(kv)
	errorFields := getErrorFields(err)
	for k, v := range errorFields {
		fields[k] = v
	}
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.WithFields(fields).Errorln(args...)
		return
	}
	l.logrus.WithFields(fields).Errorln(args...)
}

func (l *logger) WarnKV(ctx context.Context, kv KV, args ...interface{}) {
	fields := convertKvsToLogrusFields(kv)
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.WithFields(fields).Warnln(args...)
	}
	l.logrus.WithFields(fields).Warnln(args...)
}

func (l *logger) InfoKV(ctx context.Context, kv KV, args ...interface{}) {
	fields := convertKvsToLogrusFields(kv)
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.WithFields(fields).Info(args...)
	}
	l.logrus.WithFields(fields).Info(args...)
}

func (l *logger) DebugKV(ctx context.Context, kv KV, args ...interface{}) {
	fields := convertKvsToLogrusFields(kv)
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.WithFields(fields).Debug(args...)
		return
	}
	l.logrus.WithFields(fields).Debug(args...)
}

func (l *logger) ErrorKVf(ctx context.Context, err error, kv KV, message string, args ...interface{}) {
	fields := convertKvsToLogrusFields(kv)
	errorFields := getErrorFields(err)
	for k, v := range errorFields {
		fields[k] = v
	}
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.WithFields(fields).Errorf(message, args...)
		return
	}
	l.logrus.WithFields(fields).Errorf(message, args...)
}

func (l *logger) WarnKVf(ctx context.Context, kv KV, message string, args ...interface{}) {
	fields := convertKvsToLogrusFields(kv)
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.WithFields(fields).Warnf(message, args...)
		return
	}
	l.logrus.WithFields(fields).Warnf(message, args...)
}

func (l *logger) InfoKVf(ctx context.Context, kv KV, message string, args ...interface{}) {
	fields := convertKvsToLogrusFields(kv)
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.WithFields(fields).Infof(message, args...)
		return
	}
	l.logrus.WithFields(fields).Infof(message, args...)
}

func (l *logger) DebugKVf(ctx context.Context, kv KV, message string, args ...interface{}) {
	fields := convertKvsToLogrusFields(kv)
	log := l.getStructuredLogEntry(ctx)
	if log != nil {
		log.logger.WithFields(fields).Debugf(message, args...)
		return
	}
	l.logrus.WithFields(fields).Debugf(message, args...)
}

// Implementation of ErrorLogger interface{} with clerrors custom error
func getErrorFields(err error) logrus.Fields {
	type causer interface {
		Cause() error
	}

	var (
		stacks    interface{}
		rootCause interface{}
	)

	errCode := 0
	errTitle := ""
	statusCode := 0
	grpcCode := codes.Unknown
	errMsg := err.Error()
	messages := ""
	errStacktrace, ok := err.(rzerrors.StackTracer)
	if ok {
		stacks = errStacktrace.GetStackAsJSON()
	}

	errWithName, ok := err.(rzerrors.ErrorID)
	if ok {
		errTitle = errWithName.ID()
	}

	httpError, ok := err.(rzerrors.HTTPError)
	if ok {
		statusCode = httpError.StatusCode()
	}

	grpcError, ok := err.(rzerrors.GRPCError)
	if ok {
		grpcCode = grpcError.GRPCCode()
	}

	errorFormatter, ok := err.(rzerrors.ErrorFormatter)
	if ok {
		errMsg = errorFormatter.GetFormattedMessage()
	}

	errorCauser, ok := err.(causer)
	if ok {
		rootCause = errorCauser.Cause()
	}

	messages = rzerrors.GetErrorMessages(err)
	fields := logrus.Fields{
		"error_message": errMsg,
		"root_cause":    rootCause,
		"stacktrace":    stacks,
		"messages":      messages,
	}

	if errCode != 0 {
		fields["error_code"] = errCode
	}

	if statusCode != 0 {
		fields["status_code"] = statusCode
	}

	if grpcCode != codes.Unknown {
		fields["grpc_code"] = grpcCode
	}

	if errTitle != "" {
		fields["error_title"] = errTitle
	}

	return fields
}

func convertKvsToLogrusFields(kv KV) logrus.Fields {
	fields := make(logrus.Fields)
	for k, v := range kv {
		fields[k] = v
	}
	return fields
}

func (logg *logger) getStructuredLogEntry(ctx context.Context) *StructuredLoggerEntry {
	log := ctx.Value(LogEntryCtxKey{})
	if log == nil {
		log = ctx.Value(LogEntryCtxKey{}.String())
	}
	if log != nil {

		logEntry, _ := log.(*StructuredLoggerEntry)
		logEntry.logger = logEntry.logger.WithField("@timestamp", time.Now().Format(time.RFC3339Nano))
		return logEntry
	}

	loggerInterface := ctx.Value(LoggerCtxKey{})
	if loggerInterface != nil {
		logger := loggerInterface.(*logger)
		return &StructuredLoggerEntry{
			logger: logger.logrus,
		}
	}
	return nil
}

func (logger *logger) NewRequestLogger() func(next http.Handler) http.Handler {
	f := &RequestStructureLogger{logger.logrus}
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			entry := f.NewLogEntry(r)
			ww := NewWrapResponseWriter(w, r.ProtoMajor)

			t1 := time.Now()
			defer func() {
				entry.Write(ww.Status(), ww.BytesWritten(), ww.Header(), time.Since(t1), nil)
			}()

			next.ServeHTTP(ww, withLogEntry(r, entry))
		}
		return http.HandlerFunc(fn)
	}
}

func withLogEntry(r *http.Request, entry LogEntry) *http.Request {
	r = r.WithContext(context.WithValue(r.Context(), LogEntryCtxKey{}, entry))
	return r
}

func (logger *logger) UnaryServerInterceptor(opts ...Option) grpc.UnaryServerInterceptor {
	o := evaluateServerOpt(opts)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		entry := logrus.NewEntry(logger.logrus)
		startTime := time.Now()

		newCtx := newLoggerForCall(ctx, entry, "unary", info.FullMethod, startTime, o.timestampFormat)
		resp, err := handler(newCtx, req)

		if !o.shouldLog(info.FullMethod, err) {
			return resp, err
		}
		code := o.codeFunc(err)
		level := o.levelFunc(code)
		durField, durVal := o.durationFunc(time.Since(startTime))
		fields := logrus.Fields{
			"grpc.code": code.String(),
			durField:    durVal,
		}
		if err != nil {
			fields[logrus.ErrorKey] = err
		}
		o.messageFunc(newCtx, "gRPC.nary call completed", level, code, err, fields)
		return resp, err
	}
}

func (logger *logger) StreamServerInterceptor(opts ...Option) grpc.StreamServerInterceptor {
	o := evaluateServerOpt(opts)
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		entry := logrus.NewEntry(logger.logrus)
		startTime := time.Now()

		newCtx := newLoggerForCall(stream.Context(), entry, info.FullMethod, "stream", startTime, o.timestampFormat)
		wrapped := grpc_middleware.WrapServerStream(stream)
		wrapped.WrappedContext = newCtx

		err := handler(srv, wrapped)

		if !o.shouldLog(info.FullMethod, err) {
			return err
		}
		code := o.codeFunc(err)
		level := o.levelFunc(code)
		durField, durVal := o.durationFunc(time.Since(startTime))
		fields := logrus.Fields{
			"grpc.code": code.String(),
			durField:    durVal,
		}

		o.messageFunc(newCtx, "gRPC.stream call completed", level, code, err, fields)
		return err
	}
}

func newLoggerForCall(ctx context.Context, entry *logrus.Entry, callType string, fullMethodString string, start time.Time, timestampFormat string) context.Context {
	service := path.Dir(fullMethodString)[1:]
	method := path.Base(fullMethodString)
	callLog := entry.WithFields(
		logrus.Fields{
			_systemField:      "grpc",
			_kindField:        "server",
			"grpc.service":    service,
			"grpc.method":     method,
			"grpc.start_time": start.Format(timestampFormat),
		})

	if d, ok := ctx.Deadline(); ok {
		callLog = callLog.WithFields(
			logrus.Fields{
				"grpc.request.deadline": d.Format(timestampFormat),
			})
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		reqIDValues := md.Get(rzmiddleware.RequestIDHeader)
		if len(reqIDValues) > 0 {
			reqID := reqIDValues[0]
			callLog = callLog.WithFields(
				logrus.Fields{
					"req_id": reqID,
				})
		}
	}
	callLog.Infoln(fmt.Sprintf("gRPC.%s call started", callType))
	return context.WithValue(ctx, LogEntryCtxKey{}, &StructuredLoggerEntry{logger: callLog})
}
