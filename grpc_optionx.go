// Copyright 2017 Michal Witkowski. All Rights Reserved.
// See LICENSE for licensing terms.

package rzlog

import (
	"context"
	"time"

	"github.com/kyawmyintthein/rzerrors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type (
	ErrorToCode func(err error) codes.Code
	Decider     func(fullMethodName string, err error) bool
)

var (
	defaultOptions = &options{
		levelFunc:       nil,
		shouldLog:       DefaultDeciderMethod,
		codeFunc:        DefaultErrorToCode,
		durationFunc:    DefaultDurationToField,
		messageFunc:     DefaultMessageProducer,
		timestampFormat: time.RFC3339Nano,
	}
)

type options struct {
	levelFunc       CodeToLevel
	shouldLog       Decider
	codeFunc        ErrorToCode
	durationFunc    DurationToField
	messageFunc     MessageProducer
	timestampFormat string
}

func evaluateServerOpt(opts []Option) *options {
	optCopy := &options{}
	*optCopy = *defaultOptions
	optCopy.levelFunc = DefaultCodeToLevel
	for _, o := range opts {
		o(optCopy)
	}
	return optCopy
}

func evaluateClientOpt(opts []Option) *options {
	optCopy := &options{}
	*optCopy = *defaultOptions
	optCopy.levelFunc = DefaultClientCodeToLevel
	for _, o := range opts {
		o(optCopy)
	}
	return optCopy
}

type Option func(*options)

// CodeToLevel function defines the mapping between gRPC return codes and interceptor log level.
type CodeToLevel func(code codes.Code) logrus.Level

// DurationToField function defines how to produce duration fields for logging
type DurationToField func(duration time.Duration) (key string, value interface{})

// WithDecider customizes the function for deciding if the gRPC interceptor logs should log.
func WithDecider(f Decider) Option {
	return func(o *options) {
		o.shouldLog = f
	}
}

// WithLevels customizes the function for mapping gRPC return codes and interceptor log level statements.
func WithLevels(f CodeToLevel) Option {
	return func(o *options) {
		o.levelFunc = f
	}
}

// WithCodes customizes the function for mapping errors to error codes.
func WithCodes(f ErrorToCode) Option {
	return func(o *options) {
		o.codeFunc = f
	}
}

// WithDurationField customizes the function for mapping request durations to log fields.
func WithDurationField(f DurationToField) Option {
	return func(o *options) {
		o.durationFunc = f
	}
}

// WithMessageProducer customizes the function for message formation.
func WithMessageProducer(f MessageProducer) Option {
	return func(o *options) {
		o.messageFunc = f
	}
}

// WithTimestampFormat customizes the timestamps emitted in the log fields.
func WithTimestampFormat(format string) Option {
	return func(o *options) {
		o.timestampFormat = format
	}
}

// DefaultCodeToLevel is the default implementation of gRPC return codes to log levels for server side.
func DefaultCodeToLevel(code codes.Code) logrus.Level {
	switch code {
	case codes.OK:
		return logrus.InfoLevel
	case codes.Canceled:
		return logrus.InfoLevel
	case codes.Unknown:
		return logrus.ErrorLevel
	case codes.InvalidArgument:
		return logrus.InfoLevel
	case codes.DeadlineExceeded:
		return logrus.WarnLevel
	case codes.NotFound:
		return logrus.InfoLevel
	case codes.AlreadyExists:
		return logrus.InfoLevel
	case codes.PermissionDenied:
		return logrus.WarnLevel
	case codes.Unauthenticated:
		return logrus.InfoLevel // unauthenticated requests can happen
	case codes.ResourceExhausted:
		return logrus.WarnLevel
	case codes.FailedPrecondition:
		return logrus.WarnLevel
	case codes.Aborted:
		return logrus.WarnLevel
	case codes.OutOfRange:
		return logrus.WarnLevel
	case codes.Unimplemented:
		return logrus.ErrorLevel
	case codes.Internal:
		return logrus.ErrorLevel
	case codes.Unavailable:
		return logrus.WarnLevel
	case codes.DataLoss:
		return logrus.ErrorLevel
	default:
		return logrus.ErrorLevel
	}
}

// DefaultClientCodeToLevel is the default implementation of gRPC return codes to log levels for client side.
func DefaultClientCodeToLevel(code codes.Code) logrus.Level {
	switch code {
	case codes.OK:
		return logrus.DebugLevel
	case codes.Canceled:
		return logrus.DebugLevel
	case codes.Unknown:
		return logrus.InfoLevel
	case codes.InvalidArgument:
		return logrus.DebugLevel
	case codes.DeadlineExceeded:
		return logrus.InfoLevel
	case codes.NotFound:
		return logrus.DebugLevel
	case codes.AlreadyExists:
		return logrus.DebugLevel
	case codes.PermissionDenied:
		return logrus.InfoLevel
	case codes.Unauthenticated:
		return logrus.InfoLevel // unauthenticated requests can happen
	case codes.ResourceExhausted:
		return logrus.DebugLevel
	case codes.FailedPrecondition:
		return logrus.DebugLevel
	case codes.Aborted:
		return logrus.DebugLevel
	case codes.OutOfRange:
		return logrus.DebugLevel
	case codes.Unimplemented:
		return logrus.WarnLevel
	case codes.Internal:
		return logrus.WarnLevel
	case codes.Unavailable:
		return logrus.WarnLevel
	case codes.DataLoss:
		return logrus.WarnLevel
	default:
		return logrus.InfoLevel
	}
}

// DefaultDurationToField is the default implementation of converting request duration to a log field (key and value).
var DefaultDurationToField = DurationToTimeMillisField

// DurationToTimeMillisField converts the duration to milliseconds and uses the key `grpc.time_ms`.
func DurationToTimeMillisField(duration time.Duration) (key string, value interface{}) {
	return "grpc.time_ms", durationToMilliseconds(duration)
}

// DurationToDurationField uses the duration value to log the request duration.
func DurationToDurationField(duration time.Duration) (key string, value interface{}) {
	return "grpc.duration", duration
}

func durationToMilliseconds(duration time.Duration) float32 {
	return float32(duration.Nanoseconds()/1000) / 1000
}

// MessageProducer produces a user defined log message
type MessageProducer func(ctx context.Context, format string, level logrus.Level, code codes.Code, err error, fields logrus.Fields)

// DefaultMessageProducer writes the default message
func DefaultMessageProducer(ctx context.Context, format string, level logrus.Level, code codes.Code, err error, fields logrus.Fields) {
	if err != nil {
		errData := make(map[string]interface{})
		errData["message"] = err.Error()
		errWithFormatter, ok := err.(rzerrors.ErrorFormatter)
		if ok {
			errData["message"] = errWithFormatter.GetFormattedMessage()
			errData["args"] = errWithFormatter.GetArgs()
		}
		errWithID, ok := err.(rzerrors.ErrorID)
		if ok {
			errData["error_code"] = errWithID.ID()
		}

		fields[logrus.ErrorKey] = errData
		if level == logrus.DebugLevel {
			errData["stacktrace"] = rzerrors.GetErrorMessagesWithStack(err)
		}
	}

	log := ctx.Value(LogEntryCtxKey{})
	if log == nil {
		log = ctx.Value(LogEntryCtxKey{}.String())
	}
	if log != nil {
		logEntry, _ := log.(*StructuredLoggerEntry)
		logEntry.logger = logEntry.logger.WithField("@timestamp", time.Now().Format(time.RFC3339Nano))
		entry := logEntry.logger.WithFields(fields)
		switch level {
		case logrus.DebugLevel:
			entry.Debugf(format)
		case logrus.InfoLevel:
			entry.Infof(format)
		case logrus.WarnLevel:
			entry.Warningf(format)
		case logrus.ErrorLevel:
			entry.Errorf(format)
		case logrus.FatalLevel:
			entry.Fatalf(format)
		case logrus.PanicLevel:
			entry.Panicf(format)
		}
	}
}

func DefaultErrorToCode(err error) codes.Code {
	errWithGRPCCode, ok := err.(rzerrors.GRPCError)
	if ok {
		return errWithGRPCCode.GRPCCode()
	}

	stat := status.Convert(err)
	return stat.Code()
}

func DefaultDeciderMethod(fullMethodName string, err error) bool {
	return true
}
