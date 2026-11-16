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

func TestTegraDevice_GetUUID(t *testing.T) {
	dev := &tegraDevice{}
	uuid, err := dev.GetUUID()
	require.NoError(t, err)
	require.Equal(t, "tegra", uuid)
}

func TestTegraDevice_Methods(t *testing.T) {
	dev := &tegraDevice{}

	paths, err := dev.GetPaths()
	require.NoError(t, err)
	require.Nil(t, paths)

	hasNuma, numa, err := dev.GetNumaNode()
	require.NoError(t, err)
	require.False(t, hasNuma)
	require.Equal(t, -1, numa)

	mem, err := dev.GetTotalMemory()
	require.NoError(t, err)
	require.Equal(t, uint64(0), mem)

	cc, err := dev.GetComputeCapability()
	require.NoError(t, err)
	require.Equal(t, "0.0", cc)
}

func TestBuildTegraDeviceMap(t *testing.T) {
	testCases := []struct {
		description   string
		config        *spec.Config
		expectedCount int
	}{
		{
			description: "matching resource pattern includes tegra device",
			config: &spec.Config{
				Resources: spec.Resources{
					GPUs: []spec.Resource{
						{
							Name:    "nvidia.com/gpu",
							Pattern: spec.ResourcePattern("*"),
						},
					},
				},
			},
			expectedCount: 1,
		},
		{
			description: "non-matching resource pattern produces empty device map",
			config: &spec.Config{
				Resources: spec.Resources{
					GPUs: []spec.Resource{
						{
							Name:    "nvidia.com/gpu",
							Pattern: spec.ResourcePattern("discrete-only"),
						},
					},
				},
			},
			expectedCount: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			deviceMap, err := buildTegraDeviceMap(tc.config)
			require.NoError(t, err)
			var total int
			for _, devices := range deviceMap {
				total += len(devices)
			}
			require.Equal(t, tc.expectedCount, total)
		})
	}
}
