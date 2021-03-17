// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package zipkinreceiver

import (
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	jaegerzipkin "github.com/jaegertracing/jaeger/model/converter/thrift/zipkin"
	zipkinmodel "github.com/openzipkin/zipkin-go/model"
	"github.com/openzipkin/zipkin-go/proto/zipkin_proto3"
	"go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"go.opentelemetry.io/collector/client"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenterror"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/pdata"
	"go.opentelemetry.io/collector/obsreport"
	"go.opentelemetry.io/collector/translator/trace/zipkin"
)

const (
	receiverTransportV1Thrift = "http_v1_thrift"
	receiverTransportV1JSON   = "http_v1_json"
	receiverTransportV2JSON   = "http_v2_json"
	receiverTransportV2PROTO  = "http_v2_proto"
)

var errNextConsumerRespBody = []byte(`"Internal Server Error"`)

// ZipkinReceiver type is used to handle spans received in the Zipkin format.
type ZipkinReceiver struct {
	// mu protects the fields of this struct
	mu sync.Mutex

	// addr is the address onto which the HTTP server will be bound
	host         component.Host
	nextConsumer consumer.TracesConsumer
	instanceName string

	startOnce sync.Once
	stopOnce  sync.Once
	server    *http.Server
	config    *Config
	b3 b3.B3
}

var _ http.Handler = (*ZipkinReceiver)(nil)

// New creates a new zipkinreceiver.ZipkinReceiver reference.
func New(config *Config, nextConsumer consumer.TracesConsumer) (*ZipkinReceiver, error) {
	if nextConsumer == nil {
		return nil, componenterror.ErrNilNextConsumer
	}

	zr := &ZipkinReceiver{
		nextConsumer: nextConsumer,
		instanceName: config.Name(),
		config:       config,
		b3: b3.B3{},
	}
	return zr, nil
}

// Start spins up the receiver's HTTP server and makes the receiver start its processing.
func (zr *ZipkinReceiver) Start(ctx context.Context, host component.Host) error {
	if host == nil {
		return errors.New("nil host")
	}

	zr.mu.Lock()
	defer zr.mu.Unlock()

	var err = componenterror.ErrAlreadyStarted

	zr.startOnce.Do(func() {
		err = nil
		zr.host = host
		zr.server = zr.config.HTTPServerSettings.ToServer(zr)
		var listener net.Listener
		listener, err = zr.config.HTTPServerSettings.ToListener()
		if err != nil {
			host.ReportFatalError(err)
			return
		}
		go func() {
			err = zr.server.Serve(listener)
			if err != nil {
				host.ReportFatalError(err)
			}
		}()
	})

	return err
}

// v1ToTraceSpans parses Zipkin v1 JSON traces and converts them to OpenCensus Proto spans.
func (zr *ZipkinReceiver) v1ToTraceSpans(blob []byte, hdr http.Header) (reqs pdata.Traces, err error) {
	if hdr.Get("Content-Type") == "application/x-thrift" {
		zSpans, err := jaegerzipkin.DeserializeThrift(blob)
		if err != nil {
			return pdata.NewTraces(), err
		}

		return zipkin.V1ThriftBatchToInternalTraces(zSpans)
	}
	return zipkin.V1JSONBatchToInternalTraces(blob, zr.config.ParseStringTags)
}

// v2ToTraceSpans parses Zipkin v2 JSON or Protobuf traces and converts them to OpenCensus Proto spans.
func (zr *ZipkinReceiver) v2ToTraceSpans(blob []byte, hdr http.Header) (reqs pdata.Traces, err error) {
	// This flag's reference is from:
	//      https://github.com/openzipkin/zipkin-go/blob/3793c981d4f621c0e3eb1457acffa2c1cc591384/proto/v2/zipkin.proto#L154
	debugWasSet := hdr.Get("X-B3-Flags") == "1"

	var zipkinSpans []*zipkinmodel.SpanModel

	// Zipkin can send protobuf via http
	switch hdr.Get("Content-Type") {
	// TODO: (@odeke-em) record the unique types of Content-Type uploads
	case "application/x-protobuf":
		zipkinSpans, err = zipkin_proto3.ParseSpans(blob, debugWasSet)

	default: // By default, we'll assume using JSON
		zipkinSpans, err = zr.deserializeFromJSON(blob)
	}

	if err != nil {
		return pdata.Traces{}, err
	}

	return zipkin.V2SpansToInternalTraces(zipkinSpans, zr.config.ParseStringTags)
}

func (zr *ZipkinReceiver) deserializeFromJSON(jsonBlob []byte) (zs []*zipkinmodel.SpanModel, err error) {
	if err = json.Unmarshal(jsonBlob, &zs); err != nil {
		return nil, err
	}
	return zs, nil
}

// Shutdown tells the receiver that should stop reception,
// giving it a chance to perform any necessary clean-up and shutting down
// its HTTP server.
func (zr *ZipkinReceiver) Shutdown(context.Context) error {
	var err = componenterror.ErrAlreadyStopped
	zr.stopOnce.Do(func() {
		err = zr.server.Close()
	})
	return err
}

// processBodyIfNecessary checks the "Content-Encoding" HTTP header and if
// a compression such as "gzip", "deflate", "zlib", is found, the body will
// be uncompressed accordingly or return the body untouched if otherwise.
// Clients such as Zipkin-Java do this behavior e.g.
//    send "Content-Encoding":"gzip" of the JSON content.
func processBodyIfNecessary(req *http.Request) io.Reader {
	switch req.Header.Get("Content-Encoding") {
	default:
		return req.Body

	case "gzip":
		return gunzippedBodyIfPossible(req.Body)

	case "deflate", "zlib":
		return zlibUncompressedbody(req.Body)
	}
}

func gunzippedBodyIfPossible(r io.Reader) io.Reader {
	gzr, err := gzip.NewReader(r)
	if err != nil {
		// Just return the old body as was
		return r
	}
	return gzr
}

func zlibUncompressedbody(r io.Reader) io.Reader {
	zr, err := zlib.NewReader(r)
	if err != nil {
		// Just return the old body as was
		return r
	}
	return zr
}

const (
	zipkinV1TagValue = "zipkinV1"
	zipkinV2TagValue = "zipkinV2"
)

// The ZipkinReceiver receives spans from endpoint /api/v2 as JSON,
// unmarshals them and sends them along to the nextConsumer.
func (zr *ZipkinReceiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if c, ok := client.FromHTTP(r); ok {
		ctx = client.NewContext(ctx, c)
	}

	if strings.Contains(r.URL.Path, "/ext_cap/response") {
		zr.extCapResponseCapture(ctx, w, r)
		return
	}

	// Now deserialize and process the spans.
	asZipkinv1 := r.URL != nil && strings.Contains(r.URL.Path, "api/v1/spans")

	transportTag := transportType(r)
	ctx = obsreport.ReceiverContext(ctx, zr.instanceName, transportTag)
	ctx = obsreport.StartTraceDataReceiveOp(ctx, zr.instanceName, transportTag)

	pr := processBodyIfNecessary(r)
	slurp, _ := ioutil.ReadAll(pr)
	if c, ok := pr.(io.Closer); ok {
		_ = c.Close()
	}
	_ = r.Body.Close()

	var td pdata.Traces
	var err error
	if asZipkinv1 {
		td, err = zr.v1ToTraceSpans(slurp, r.Header)
	} else {
		td, err = zr.v2ToTraceSpans(slurp, r.Header)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	consumerErr := zr.nextConsumer.ConsumeTraces(ctx, td)

	receiverTagValue := zipkinV2TagValue
	if asZipkinv1 {
		receiverTagValue = zipkinV1TagValue
	}
	obsreport.EndTraceDataReceiveOp(ctx, receiverTagValue, td.SpanCount(), consumerErr)

	if consumerErr != nil {
		// Transient error, due to some internal condition.
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(errNextConsumerRespBody)
		return
	}

	// Finally send back the response "Accepted" as
	// required at https://zipkin.io/zipkin-api/#/default/post_spans
	w.WriteHeader(http.StatusAccepted)
}

func transportType(r *http.Request) string {
	v1 := r.URL != nil && strings.Contains(r.URL.Path, "api/v1/spans")
	if v1 {
		if r.Header.Get("Content-Type") == "application/x-thrift" {
			return receiverTransportV1Thrift
		}
		return receiverTransportV1JSON
	}
	if r.Header.Get("Content-Type") == "application/x-protobuf" {
		return receiverTransportV2PROTO
	}
	return receiverTransportV2JSON
}

// This function is used by ambassador to capture response data.
// Ambassador makes a request to this endpoint with response headers (including B3 propagation) and
// response payload. This function processes the request and creates a raw span containing headers
// and payload and sends it to the pipeline.
// see https://github.com/Traceableai/traceable-agent/issues/158
func (zr *ZipkinReceiver) extCapResponseCapture(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	ctx = zr.b3.Extract(ctx, &headerCarrier{r.Header})
	spanContext := trace.RemoteSpanContextFromContext(ctx)
	if !spanContext.IsSampled() {
		// no correlation skip storing the trace
		w.WriteHeader(http.StatusOK)
		return
	}

	td := pdata.NewTraces()
	td.ResourceSpans().Resize(1)
	rss := td.ResourceSpans().At(0)
	rss.InstrumentationLibrarySpans().Resize(1)
	rss.InstrumentationLibrarySpans().At(0).Spans().Resize(1)
	span := rss.InstrumentationLibrarySpans().At(0).Spans().At(0)

	rss.Resource().Attributes().InitFromMap(map[string]pdata.AttributeValue{
		"service.name": pdata.NewAttributeValueString("ext_cap"),
	})

	span.SetName("ext_cap/response")
	span.SetTraceID(pdata.NewTraceID(spanContext.TraceID))
	span.SetSpanID(pdata.NewSpanID(spanContext.SpanID))

	attributes := createAttributes(r)
	span.Attributes().InitFromMap(attributes)
	span.SetKind(pdata.SpanKindSERVER)
	ts := pdata.TimestampFromTime(time.Now())
	span.SetStartTime(ts)
	span.SetEndTime(ts)

	receiverTag := "ext-cap"
	ctx = obsreport.ReceiverContext(ctx, zr.instanceName, receiverTag)
	ctx = obsreport.StartTraceDataReceiveOp(ctx, zr.instanceName, receiverTag)

	consumerErr := zr.nextConsumer.ConsumeTraces(context.Background(), td)
	obsreport.EndTraceDataReceiveOp(ctx, receiverTag, td.SpanCount(), consumerErr)
	if consumerErr != nil {
		// Transient error, due to some internal condition.
		w.WriteHeader(http.StatusInternalServerError)
		w.Write(errNextConsumerRespBody)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func createAttributes(r *http.Request) map[string]pdata.AttributeValue {
	attrs := make(map[string]pdata.AttributeValue, len(r.Header) + 2)
	attrs["traceableai.merge-data"] = pdata.NewAttributeValueString("ext_cap")

	for k, v := range r.Header {
		if len(v) > 0 {
			attrs["http.response.header." + strings.ToLower(k)] = pdata.NewAttributeValueString(v[0])
		}
	}
	bodyBytes, err := ioutil.ReadAll(r.Body)
	if err != nil {
		attrs["http.response.body"] = pdata.NewAttributeValueString(string(bodyBytes))
	}
	return attrs
}

type headerCarrier struct {
	headers http.Header
}

var _ propagation.TextMapCarrier = (*headerCarrier)(nil)

func (h *headerCarrier) Get(key string) string {
	return h.headers.Get(key)
}

func (h *headerCarrier) Set(key string, value string) {
	h.headers.Set(key, value)
}

func (h *headerCarrier) Keys() []string {
	keys := make([]string, len(h.headers))
	i := 0
	for k, _ := range h.headers {
		keys[i] = k
		i++
	}
	return keys
}
