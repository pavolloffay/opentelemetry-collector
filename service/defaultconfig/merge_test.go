// Copyright 2020 The OpenTelemetry Authors
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

package defaultconfig

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/config"
	"go.opentelemetry.io/collector/config/configgrpc"
	"go.opentelemetry.io/collector/config/confighttp"
	"go.opentelemetry.io/collector/config/configmodels"
	"go.opentelemetry.io/collector/config/confignet"
	"go.opentelemetry.io/collector/processor/attributesprocessor"
	"go.opentelemetry.io/collector/processor/batchprocessor"
	"go.opentelemetry.io/collector/processor/processorhelper"
	"go.opentelemetry.io/collector/receiver/jaegerreceiver"
	"go.opentelemetry.io/collector/receiver/zipkinreceiver"
	"go.opentelemetry.io/collector/service/defaultcomponents"
)

func TestMergeConfigs_nil(t *testing.T) {
	cfg := &configmodels.Config{
		Receivers: configmodels.Receivers{
			"jaeger": &jaegerreceiver.Config{
				RemoteSampling: &jaegerreceiver.RemoteSamplingConfig{StrategyFile: "file.json"},
			},
		},
	}
	t.Run("src_nil", func(t *testing.T) {
		err := MergeConfigs(cfg, nil)
		require.NoError(t, err)
		assert.Equal(t, cfg, cfg)
	})
	t.Run("dest_nil", func(t *testing.T) {
		err := MergeConfigs(nil, cfg)
		require.NoError(t, err)
		assert.Equal(t, cfg, cfg)
	})
}

func TestMergeConfigs(t *testing.T) {
	cfg := &configmodels.Config{
		Receivers: configmodels.Receivers{
			"jaeger": &jaegerreceiver.Config{
				Protocols: jaegerreceiver.Protocols{
					GRPC: &configgrpc.GRPCServerSettings{
						NetAddr: confignet.NetAddr{
							Endpoint: "def",
						},
					},
					ThriftCompact: &confignet.TCPAddr{
						Endpoint: "def",
					},
				},
			},
		},
		Processors: configmodels.Processors{
			"batch": &batchprocessor.Config{
				SendBatchSize: uint32(160),
			},
		},
		Service: configmodels.Service{
			Extensions: []string{"def", "def2"},
			Pipelines: configmodels.Pipelines{
				"traces": &configmodels.Pipeline{
					Receivers:  []string{"jaeger"},
					Processors: []string{"batch"},
				},
			},
		},
	}
	overrideCfg := &configmodels.Config{
		Receivers: configmodels.Receivers{
			"jaeger": &jaegerreceiver.Config{
				Protocols: jaegerreceiver.Protocols{
					GRPC: &configgrpc.GRPCServerSettings{
						NetAddr: confignet.NetAddr{
							Endpoint: "master_jager_url",
						},
					},
				},
			},
			"zipkin": &zipkinreceiver.Config{
				HTTPServerSettings: confighttp.HTTPServerSettings{
					Endpoint: "master_zipkin_url",
				},
			},
		},
		Processors: configmodels.Processors{
			"attributes": &attributesprocessor.Config{
				Settings: processorhelper.Settings{
					Actions: []processorhelper.ActionKeyValue{{Key: "foo"}},
				},
			},
		},
		Service: configmodels.Service{
			Extensions: []string{"def", "master1", "master2"},
			Pipelines: configmodels.Pipelines{
				"traces": &configmodels.Pipeline{
					Receivers:  []string{"jaeger", "zipkin"},
					Processors: []string{"attributes"},
				},
				"traces/2": &configmodels.Pipeline{
					Processors: []string{"example"},
				},
			},
		},
	}
	expected := &configmodels.Config{
		Receivers: configmodels.Receivers{
			"jaeger": &jaegerreceiver.Config{
				Protocols: jaegerreceiver.Protocols{
					GRPC: &configgrpc.GRPCServerSettings{
						NetAddr: confignet.NetAddr{
							Endpoint: "master_jager_url",
						},
					},
					ThriftCompact: &confignet.TCPAddr{
						Endpoint: "def",
					},
				},
			},
			"zipkin": &zipkinreceiver.Config{
				HTTPServerSettings: confighttp.HTTPServerSettings{
					Endpoint: "master_zipkin_url",
				},
			},
		},
		Processors: configmodels.Processors{
			"batch": &batchprocessor.Config{
				SendBatchSize: uint32(160),
			},
			"attributes": &attributesprocessor.Config{
				Settings: processorhelper.Settings{
					Actions: []processorhelper.ActionKeyValue{{Key: "foo"}},
				},
			},
		},
		Service: configmodels.Service{
			Extensions: []string{"def", "master1", "master2"},
			Pipelines: configmodels.Pipelines{
				"traces": &configmodels.Pipeline{
					Receivers:  []string{"jaeger", "zipkin"},
					Processors: []string{"attributes"},
				},
				"traces/2": &configmodels.Pipeline{
					Processors: []string{"example"},
				},
			},
		},
	}
	err := MergeConfigs(cfg, overrideCfg)
	require.NoError(t, err)
	assert.Equal(t, expected, cfg)
}

func TestMergeConfigFiles(t *testing.T) {
	testFiles := []string{"emptyoverride", "addprocessor", "multiplecomponents"}
	cmpts, err := defaultcomponents.Components()
	require.NoError(t, err)
	for _, f := range testFiles {
		t.Run(f, func(t *testing.T) {
			cfg, err := loadConfig(cmpts, fmt.Sprintf("testdata/%s.yaml", f))
			require.NoError(t, err)
			override, err := loadConfig(cmpts, fmt.Sprintf("testdata/%s-override.yaml", f))
			require.NoError(t, err)
			merged, err := loadConfig(cmpts, fmt.Sprintf("testdata/%s-merged.yaml", f))
			require.NoError(t, err)
			err = MergeConfigs(cfg, override)
			require.NoError(t, err)
			assert.Equal(t, merged, cfg)
		})
	}
}

func loadConfig(factories component.Factories, file string) (*configmodels.Config, error) {
	v := config.NewViper()
	v.SetConfigFile(file)
	err := v.ReadInConfig()
	if err != nil {
		return nil, fmt.Errorf("error loading config file %q: %v", file, err)
	}
	return config.Load(v, factories)
}
