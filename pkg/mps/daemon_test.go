/**
# Copyright 2024 NVIDIA CORPORATION
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

package mps

import (
	"testing"

	"github.com/stretchr/testify/require"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/rm"
)

func TestDaemonEnvVars(t *testing.T) {
	testCases := []struct {
		description string
		daemon      *Daemon
		expected    map[string]string
	}{
		{
			description: "nil daemon returns nil envvars",
			daemon:      nil,
			expected:    nil,
		},
		{
			description: "daemon with nil rm returns nil envvars",
			daemon:      NewDaemon(nil, Root("/var/run/nvidia-mps")),
			expected:    nil,
		},
		{
			description: "daemon with empty devices returns pipe and log dirs without CUDA_VISIBLE_DEVICES",
			daemon: NewDaemon(&rm.ResourceManagerMock{
				ResourceFunc: func() spec.ResourceName {
					return spec.ResourceName("nvidia.com/gpu")
				},
				DevicesFunc: func() rm.Devices {
					return rm.Devices{}
				},
			}, Root("/var/run/nvidia-mps")),
			expected: map[string]string{
				"CUDA_MPS_PIPE_DIRECTORY": "/var/run/nvidia-mps/nvidia.com/gpu/pipe",
				"CUDA_MPS_LOG_DIRECTORY":  "/var/run/nvidia-mps/nvidia.com/gpu/log",
			},
		},
		{
			description: "daemon with multiple unique devices scopes CUDA_VISIBLE_DEVICES sorted",
			daemon: NewDaemon(&rm.ResourceManagerMock{
				ResourceFunc: func() spec.ResourceName {
					return spec.ResourceName("nvidia.com/gpu")
				},
				DevicesFunc: func() rm.Devices {
					return rm.Devices{
						"GPU-B": &rm.Device{
							Device: pluginapi.Device{ID: "GPU-B"},
						},
						"GPU-A": &rm.Device{
							Device: pluginapi.Device{ID: "GPU-A"},
						},
					}
				},
			}, Root("/var/run/nvidia-mps")),
			expected: map[string]string{
				"CUDA_MPS_PIPE_DIRECTORY": "/var/run/nvidia-mps/nvidia.com/gpu/pipe",
				"CUDA_MPS_LOG_DIRECTORY":  "/var/run/nvidia-mps/nvidia.com/gpu/log",
				"CUDA_VISIBLE_DEVICES":    "GPU-A,GPU-B",
			},
		},
		{
			description: "daemon with replicated devices scopes and deduplicates UUIDs",
			daemon: NewDaemon(&rm.ResourceManagerMock{
				ResourceFunc: func() spec.ResourceName {
					return spec.ResourceName("nvidia.com/gpu")
				},
				DevicesFunc: func() rm.Devices {
					return rm.Devices{
						"GPU-UUID-1::0": &rm.Device{
							Device: pluginapi.Device{ID: "GPU-UUID-1::0"},
						},
						"GPU-UUID-1::1": &rm.Device{
							Device: pluginapi.Device{ID: "GPU-UUID-1::1"},
						},
						"GPU-UUID-2::0": &rm.Device{
							Device: pluginapi.Device{ID: "GPU-UUID-2::0"},
						},
					}
				},
			}, Root("/var/run/nvidia-mps")),
			expected: map[string]string{
				"CUDA_MPS_PIPE_DIRECTORY": "/var/run/nvidia-mps/nvidia.com/gpu/pipe",
				"CUDA_MPS_LOG_DIRECTORY":  "/var/run/nvidia-mps/nvidia.com/gpu/log",
				"CUDA_VISIBLE_DEVICES":    "GPU-UUID-1,GPU-UUID-2",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			envs := tc.daemon.EnvVars()
			if tc.expected == nil {
				require.Nil(t, envs)
			} else {
				require.EqualValues(t, tc.expected, map[string]string(envs))
			}
		})
	}
}
