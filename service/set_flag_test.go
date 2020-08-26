// Copyright  The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package service

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/config/configgrpc"
	"go.opentelemetry.io/collector/config/configmodels"
	"go.opentelemetry.io/collector/config/confignet"
	"go.opentelemetry.io/collector/exporter/kafkaexporter"
	"go.opentelemetry.io/collector/processor/batchprocessor"
	"go.opentelemetry.io/collector/processor/processorhelper"
	"go.opentelemetry.io/collector/processor/resourceprocessor"
	"go.opentelemetry.io/collector/receiver/otlpreceiver"
	"go.opentelemetry.io/collector/service/defaultcomponents"
)

func TestFlags(t *testing.T) {
	cmd := &cobra.Command{}
	addSetFlag(cmd.Flags())

	err := cmd.ParseFlags([]string{
		"--set=processors.batch.timeout=2s",
		"--set=processors.batch/foo.timeout=3s",
		// no effect - the batch/bar is not defined in the config
		"--set=processors.batch/bar.timeout=3s",
		// attributes is a list of objects, The arrays are overridden
		"--set=processors.resource.attributes.key=key2",
		// TODO arrays of objects cannot be indexed
		// TODO the flag sets only the first element in the array
		//"--set=processors.resource.attributes[0].key=key3",
		// maps of primitive types are joined
		"--set=processors.resource.labels.key2=value2",
		"--set=receivers.otlp.protocols.grpc.endpoint=localhost:1818",
		// arrays of primitive types are overridden
		"--set=exporters.kafka.brokers=foo:9200,foo2:9200",
	})
	require.NoError(t, err)

	cfg := &configmodels.Config{
		Processors: map[string]configmodels.Processor{
			"batch": &batchprocessor.Config{
				ProcessorSettings: configmodels.ProcessorSettings{
					TypeVal: "batch",
					NameVal: "batch",
				},
				Timeout:       time.Second * 10,
				SendBatchSize: 10,
			},
			"batch/foo": &batchprocessor.Config{
				ProcessorSettings: configmodels.ProcessorSettings{
					TypeVal: "batch",
					NameVal: "batch/foo",
				},
				SendBatchSize: 20,
			},
			"resource": &resourceprocessor.Config{
				ProcessorSettings: configmodels.ProcessorSettings{
					TypeVal: "resource",
					NameVal: "resource",
				},
				AttributesActions: []processorhelper.ActionKeyValue{
					{Key: "key"},
				},
				Labels: map[string]string{"key": "value"},
			},
		},
		Receivers: map[string]configmodels.Receiver{
			"otlp": &otlpreceiver.Config{
				ReceiverSettings: configmodels.ReceiverSettings{
					TypeVal: "otlp",
					NameVal: "otlp",
				},
			},
		},
		Exporters: map[string]configmodels.Exporter{
			"kafka": &kafkaexporter.Config{
				ExporterSettings: configmodels.ExporterSettings{
					TypeVal: "kafka",
					NameVal: "kafka",
				},
				// This gets overridden
				Brokers: []string{"bar:9200"},
			},
		},
	}
	factories, err := defaultcomponents.Components()
	require.NoError(t, err)
	err = applyFlags(cmd, cfg, factories)
	require.NoError(t, err)
	assert.Equal(t, &configmodels.Config{
		Processors: map[string]configmodels.Processor{
			"batch": &batchprocessor.Config{
				ProcessorSettings: configmodels.ProcessorSettings{
					TypeVal: "batch",
					NameVal: "batch",
				},
				Timeout:       time.Second * 2,
				SendBatchSize: 10,
			},
			"batch/foo": &batchprocessor.Config{
				ProcessorSettings: configmodels.ProcessorSettings{
					TypeVal: "batch",
					NameVal: "batch/foo",
				},
				Timeout:       time.Second * 3,
				SendBatchSize: 20,
			},
			"resource": &resourceprocessor.Config{
				ProcessorSettings: configmodels.ProcessorSettings{
					TypeVal: "resource",
					NameVal: "resource",
				},
				AttributesActions: []processorhelper.ActionKeyValue{
					//{Key: "key"},
					{Key: "key2"},
				},
				Labels: map[string]string{"key": "value", "key2": "value2"},
			},
		},
		Receivers: map[string]configmodels.Receiver{
			"otlp": &otlpreceiver.Config{
				ReceiverSettings: configmodels.ReceiverSettings{
					TypeVal: "otlp",
					NameVal: "otlp",
				},
				Protocols: otlpreceiver.Protocols{
					GRPC: &configgrpc.GRPCServerSettings{
						NetAddr: confignet.NetAddr{
							Endpoint: "localhost:1818",
						},
					},
				},
			},
		},
		Exporters: map[string]configmodels.Exporter{
			"kafka": &kafkaexporter.Config{
				ExporterSettings: configmodels.ExporterSettings{
					TypeVal: "kafka",
					NameVal: "kafka",
				},
				Brokers: []string{"foo:9200", "foo2:9200"},
			},
		},
	}, cfg)
}

func TestFlags_component_with_custom_marshaller_is_not_defined_via_flags(t *testing.T) {
	cmd := &cobra.Command{}
	addSetFlag(cmd.Flags())

	err := cmd.ParseFlags([]string{
		"--set=processors.batch.timeout=2s",
	})
	require.NoError(t, err)

	cfg := &configmodels.Config{
		Processors: map[string]configmodels.Processor{
			"batch": &batchprocessor.Config{
				ProcessorSettings: configmodels.ProcessorSettings{
					TypeVal: "batch",
					NameVal: "batch",
				},
				Timeout: time.Second * 10,
			},
		},
		Receivers: map[string]configmodels.Receiver{
			"otlp": &otlpreceiver.Config{
				ReceiverSettings: configmodels.ReceiverSettings{
					TypeVal: "otlp",
					NameVal: "otlp",
				},
				Protocols: otlpreceiver.Protocols{
					GRPC: &configgrpc.GRPCServerSettings{
						NetAddr: confignet.NetAddr{
							Endpoint: "localhost:8090",
						},
					},
				},
			},
		},
		Exporters: map[string]configmodels.Exporter{
			"kafka": &kafkaexporter.Config{
				ExporterSettings: configmodels.ExporterSettings{
					TypeVal: "kafka",
					NameVal: "kafka",
				},
				Brokers: []string{"bar:9200"},
			},
		},
	}
	factories, err := defaultcomponents.Components()
	require.NoError(t, err)
	err = applyFlags(cmd, cfg, factories)
	require.NoError(t, err)
	assert.Equal(t, &configmodels.Config{
		Processors: map[string]configmodels.Processor{
			"batch": &batchprocessor.Config{
				ProcessorSettings: configmodels.ProcessorSettings{
					TypeVal: "batch",
					NameVal: "batch",
				},
				Timeout: time.Second * 2,
			},
		},
		Receivers: map[string]configmodels.Receiver{
			"otlp": &otlpreceiver.Config{
				ReceiverSettings: configmodels.ReceiverSettings{
					TypeVal: "otlp",
					NameVal: "otlp",
				},
				Protocols: otlpreceiver.Protocols{
					GRPC: &configgrpc.GRPCServerSettings{
						NetAddr: confignet.NetAddr{
							Endpoint: "localhost:8090",
						},
					},
				},
			},
		},
		Exporters: map[string]configmodels.Exporter{
			"kafka": &kafkaexporter.Config{
				ExporterSettings: configmodels.ExporterSettings{
					TypeVal: "kafka",
					NameVal: "kafka",
				},
				Brokers: []string{"bar:9200"},
			},
		},
	}, cfg)
}
