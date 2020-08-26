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
	"github.com/imdario/mergo"

	"go.opentelemetry.io/collector/config/configmodels"
)

// MergeConfigs merges two configs.
// The src is merged into dst.
func MergeConfigs(dst, src *configmodels.Config) error {
	if src == nil || dst == nil {
		return nil
	}
	return mergo.Merge(dst, src, mergo.WithOverride)
}
