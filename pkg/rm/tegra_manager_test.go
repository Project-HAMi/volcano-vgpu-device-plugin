/**
# Copyright (c) 2022, NVIDIA CORPORATION.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
**/

package rm

import (
	"testing"

	"github.com/stretchr/testify/require"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
)

func TestNewTegraResourceManagers(t *testing.T) {
	t.Run("matching config returns resource manager", func(t *testing.T) {
		cfg := &spec.Config{
			Resources: spec.Resources{
				GPUs: []spec.Resource{
					{
						Name:    "nvidia.com/gpu",
						Pattern: spec.ResourcePattern("*"),
					},
				},
			},
		}

		rms, err := NewTegraResourceManagers(cfg)
		require.NoError(t, err)
		require.Len(t, rms, 1)

		rm := rms[0]
		require.Equal(t, spec.ResourceName("nvidia.com/gpu"), rm.Resource())
		require.Len(t, rm.Devices(), 1)
		require.Nil(t, rm.GetDevicePaths([]string{"tegra"}))
		require.NoError(t, rm.CheckHealth(nil, nil))

		alloc, err := rm.GetPreferredAllocation([]string{"tegra"}, []string{}, 1)
		require.NoError(t, err)
		require.Equal(t, []string{"tegra"}, alloc)
	})

	t.Run("non-matching config returns empty slice", func(t *testing.T) {
		cfg := &spec.Config{
			Resources: spec.Resources{
				GPUs: []spec.Resource{
					{
						Name:    "nvidia.com/gpu",
						Pattern: spec.ResourcePattern("nonexistent"),
					},
				},
			},
		}

		rms, err := NewTegraResourceManagers(cfg)
		require.NoError(t, err)
		require.Empty(t, rms)
	})
}
