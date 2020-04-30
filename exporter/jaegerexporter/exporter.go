// Copyright 2019, OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package jaegerexporter

import (
	"context"

	jaegerproto "github.com/jaegertracing/jaeger/proto-gen/api_v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/open-telemetry/opentelemetry-collector/component"
	"github.com/open-telemetry/opentelemetry-collector/config/configgrpc"
	"github.com/open-telemetry/opentelemetry-collector/consumer/consumererror"
	"github.com/open-telemetry/opentelemetry-collector/consumer/pdata"
	"github.com/open-telemetry/opentelemetry-collector/exporter/exporterhelper"
	jaegertranslator "github.com/open-telemetry/opentelemetry-collector/translator/trace/jaeger"
)

// New returns a new Jaeger gRPC exporter.
// The exporter name is the name to be used in the observability of the exporter.
// The collectorEndpoint should be of the form "hostname:14250" (a gRPC target).
func New(config *Config) (component.TraceExporter, error) {

	opts, err := configgrpc.GrpcSettingsToDialOptions(config.GRPCSettings)
	if err != nil {
		return nil, err
	}

	client, err := grpc.Dial(config.GRPCSettings.Endpoint, opts...)
	if err != nil {
		return nil, err
	}

	collectorServiceClient := jaegerproto.NewCollectorServiceClient(client)
	s := &protoGRPCSender{
		client:   collectorServiceClient,
		metadata: metadata.New(config.GRPCSettings.Headers),
	}

	exp, err := exporterhelper.NewTraceExporter(config, s.pushTraceData)

	return exp, err
}

// protoGRPCSender forwards spans encoded in the jaeger proto
// format, to a grpc server.
type protoGRPCSender struct {
	client   jaegerproto.CollectorServiceClient
	metadata metadata.MD
}

func (s *protoGRPCSender) pushTraceData(
	ctx context.Context,
	td pdata.Traces,
) (droppedSpans int, err error) {

	batches, err := jaegertranslator.InternalTracesToJaegerProto(td)
	if err != nil {
		return td.SpanCount(), consumererror.Permanent(err)
	}

	if s.metadata.Len() > 0 {
		ctx = metadata.NewOutgoingContext(ctx, s.metadata)
	}

	var sentSpans int
	for _, batch := range batches {
		_, err = s.client.PostSpans(
			ctx,
			&jaegerproto.PostSpansRequest{Batch: *batch}, grpc.WaitForReady(true))
		if err != nil {
			return td.SpanCount() - sentSpans, err
		}
		sentSpans += len(batch.Spans)
	}

	return 0, nil
}
