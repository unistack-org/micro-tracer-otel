package otel

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.unistack.org/micro/v3/metadata"
	"go.unistack.org/micro/v3/tracer"
)

var _ tracer.Tracer = (*otelTracer)(nil)

type otelTracer struct {
	opts       tracer.Options
	tracep     *tracesdk.TracerProvider
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
}

type otelSpan struct {
	ctx       context.Context
	tracer    trace.Tracer
	tracep    *tracesdk.TracerProvider
	span      trace.Span
	opts      tracer.SpanOptions
	status    tracer.SpanStatus
	statusMsg string
}

func (ot *otelTracer) Name() string {
	return ot.opts.Name
}

func (ot *otelTracer) Flush(ctx context.Context) error {
	return ot.tracep.ForceFlush(ctx)
}

func (ot *otelTracer) Init(opts ...tracer.Option) error {
	for _, o := range opts {
		o(&ot.opts)
	}

	var to []trace.TracerOption
	if o, ok := ot.opts.Context.Value(tracerOptionsKey{}).([]trace.TracerOption); ok {
		to = o
	}

	var tp *tracesdk.TracerProvider
	if p, ok := ot.opts.Context.Value(providerKey{}).(*tracesdk.TracerProvider); ok {
		tp = p
	}

	if o, ok := ot.opts.Context.Value(providerOptionsKey{}).([]tracesdk.TracerProviderOption); ok {
		tp = tracesdk.NewTracerProvider(o...)
	}

	if tp == nil {
		return fmt.Errorf("missing Provider or ProviderOptions option")
	}

	ot.tracep = tp
	ot.tracer = tp.Tracer(ot.opts.Name, to...)
	ot.propagator = propagation.NewCompositeTextMapPropagator(
		propagation.Baggage{},
		propagation.TraceContext{},
	)

	return nil
}

func (ot *otelTracer) Start(ctx context.Context, name string, opts ...tracer.SpanOption) (context.Context, tracer.Span) {
	options := tracer.NewSpanOptions(opts...)
	var span trace.Span
	switch options.Kind {
	case tracer.SpanKindInternal, tracer.SpanKindUnspecified:
		ctx, span = ot.startSpanFromContext(ctx, name)
	case tracer.SpanKindClient, tracer.SpanKindProducer:
		ctx, span = ot.startSpanFromOutgoingContext(ctx, name)
	case tracer.SpanKindServer, tracer.SpanKindConsumer:
		ctx, span = ot.startSpanFromIncomingContext(ctx, name)
	}
	return ctx, &otelSpan{ctx: ctx, tracep: ot.tracep, tracer: ot.tracer, span: span, opts: options}
}

func (os *otelSpan) SetStatus(st tracer.SpanStatus, msg string) {
	switch st {
	case tracer.SpanStatusError:
		os.span.SetStatus(codes.Error, msg)
		os.span.RecordError(errors.New(msg))
	case tracer.SpanStatusOK:
		os.span.SetStatus(codes.Ok, msg)
	case tracer.SpanStatusUnset:
		os.span.SetStatus(codes.Unset, msg)
	}
	os.status = st
	os.statusMsg = msg
}

func (os *otelSpan) Status() (tracer.SpanStatus, string) {
	return os.status, os.statusMsg
}

func (os *otelSpan) Tracer() tracer.Tracer {
	return &otelTracer{tracep: os.tracep, tracer: os.tracer}
}

func (os *otelSpan) Finish(opts ...tracer.SpanOption) {
	if len(os.opts.Labels) > 0 {
		os.span.SetAttributes(convertLabels(os.opts.Labels...)...)
	}
	os.span.End()
}

func (os *otelSpan) AddEvent(name string, opts ...tracer.EventOption) {
	os.span.AddEvent(name)
}

func (os *otelSpan) Context() context.Context {
	return trace.ContextWithSpan(os.ctx, os.span)
}

func (os *otelSpan) SetName(name string) {
	os.span.SetName(name)
}

func (os *otelSpan) SetLabels(labels ...interface{}) {
	os.opts.Labels = labels
}

func (os *otelSpan) Kind() tracer.SpanKind {
	return os.opts.Kind
}

func (os *otelSpan) AddLabels(labels ...interface{}) {
	os.opts.Labels = append(os.opts.Labels, labels...)
}

func NewTracer(opts ...tracer.Option) *otelTracer {
	options := tracer.NewOptions(opts...)
	return &otelTracer{opts: options}
}

func (ot *otelTracer) startSpanFromContext(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	md, ok := metadata.FromContext(ctx)
	if !ok {
		md = metadata.New(1)
	}

	mc := make(propagation.MapCarrier)
	for k, v := range md {
		for _, f := range ot.propagator.Fields() {
			if strings.EqualFold(k, f) {
				mc[f] = v
			}
		}
	}

	ctx = ot.propagator.Extract(ctx, mc)
	spanCtx := trace.SpanContextFromContext(ctx)
	ctx = baggage.ContextWithBaggage(ctx, baggage.FromContext(ctx))

	var span trace.Span
	ctx, span = ot.tracer.Start(trace.ContextWithRemoteSpanContext(ctx, spanCtx), name, opts...)

	mc = make(propagation.MapCarrier)
	ot.propagator.Inject(ctx, mc)
	for k, v := range mc {
		md.Set(k, v)
	}
	ctx = metadata.NewContext(ctx, md)

	return ctx, span
}

func (ot *otelTracer) startSpanFromOutgoingContext(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		md = metadata.New(1)
	}

	mc := make(propagation.MapCarrier)
	for k, v := range md {
		for _, f := range ot.propagator.Fields() {
			if strings.EqualFold(k, f) {
				mc[f] = v
			}
		}
	}

	ctx = ot.propagator.Extract(ctx, mc)
	spanCtx := trace.SpanContextFromContext(ctx)
	ctx = baggage.ContextWithBaggage(ctx, baggage.FromContext(ctx))

	var span trace.Span
	ctx, span = ot.tracer.Start(trace.ContextWithRemoteSpanContext(ctx, spanCtx), name, opts...)

	mc = make(propagation.MapCarrier)
	ot.propagator.Inject(ctx, mc)
	for k, v := range mc {
		md.Set(k, v)
	}
	ctx = metadata.NewOutgoingContext(ctx, md)

	return ctx, span
}

func (ot *otelTracer) startSpanFromIncomingContext(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		md = metadata.New(1)
	}

	mc := make(propagation.MapCarrier)
	for k, v := range md {
		for _, f := range ot.propagator.Fields() {
			if strings.EqualFold(k, f) {
				mc[f] = v
			}
		}
	}

	ctx = ot.propagator.Extract(ctx, mc)
	spanCtx := trace.SpanContextFromContext(ctx)
	ctx = baggage.ContextWithBaggage(ctx, baggage.FromContext(ctx))

	var span trace.Span
	ctx, span = ot.tracer.Start(trace.ContextWithRemoteSpanContext(ctx, spanCtx), name, opts...)

	mc = make(propagation.MapCarrier)
	ot.propagator.Inject(ctx, mc)
	for k, v := range mc {
		md.Set(k, v)
	}
	ctx = metadata.NewIncomingContext(ctx, md)

	return ctx, span
}

func convertLabels(labels ...interface{}) []attribute.KeyValue {
	kv := make([]attribute.KeyValue, 0, len(labels)/2)
	for idx := 0; idx < len(labels)/2; idx += 2 {
		if v, ok := labels[idx].(attribute.KeyValue); ok {
			kv = append(kv, v)
		}
		switch v := labels[idx+1].(type) {
		case attribute.KeyValue:
			kv = append(kv, v)
		case bool:
			kv = append(kv, attribute.Bool(labels[idx].(string), v))
		case []bool:
			kv = append(kv, attribute.BoolSlice(labels[idx].(string), v))
		case float64:
			kv = append(kv, attribute.Float64(labels[idx].(string), v))
		case []float64:
			kv = append(kv, attribute.Float64Slice(labels[idx].(string), v))
		case int:
			kv = append(kv, attribute.Int(labels[idx].(string), v))
		case []int:
			kv = append(kv, attribute.IntSlice(labels[idx].(string), v))
		case int64:
			kv = append(kv, attribute.Int64(labels[idx].(string), v))
		case []int64:
			kv = append(kv, attribute.Int64Slice(labels[idx].(string), v))
		case string:
			kv = append(kv, attribute.String(labels[idx].(string), v))
		case []string:
			kv = append(kv, attribute.StringSlice(labels[idx].(string), v))
		case fmt.Stringer:
			kv = append(kv, attribute.Stringer(labels[idx].(string), v))
		default:
			kv = append(kv, attribute.String(labels[idx].(string), fmt.Sprintf("%v", v)))
		}
	}

	return kv
}
