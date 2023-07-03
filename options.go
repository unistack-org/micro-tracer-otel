package otel

import (
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.unistack.org/micro/v4/tracer"
)

type providerKey struct{}

func Provider(opts ...tracesdk.TracerProvider) tracer.Option {
	return tracer.SetOption(providerKey{}, opts)
}

type providerOptionsKey struct{}

func ProviderOptions(opts ...tracesdk.TracerProviderOption) tracer.Option {
	return tracer.SetOption(providerOptionsKey{}, opts)
}

type tracerOptionsKey struct{}

func TracerOptions(opts ...trace.TracerOption) tracer.Option {
	return tracer.SetOption(tracerOptionsKey{}, opts)
}
