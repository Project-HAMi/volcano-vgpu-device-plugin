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

package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/mps"
	"volcano.sh/k8s-device-plugin/pkg/rm"
)

func TestUpdateResponse(t *testing.T) {
	testCases := []struct {
		description     string
		opts            *mpsOptions
		deviceIDs       []string
		initialResponse *pluginapi.ContainerAllocateResponse
		expected        *pluginapi.ContainerAllocateResponse
	}{
		{
			description:     "nil options leaves response untouched",
			opts:            nil,
			initialResponse: &pluginapi.ContainerAllocateResponse{},
			expected:        &pluginapi.ContainerAllocateResponse{},
		},
		{
			description: "disabled options leaves response untouched",
			opts: &mpsOptions{
				enabled: false,
			},
			initialResponse: &pluginapi.ContainerAllocateResponse{},
			expected:        &pluginapi.ContainerAllocateResponse{},
		},
		{
			description: "nil daemon leaves response untouched",
			opts: &mpsOptions{
				enabled: true,
				daemon:  nil,
			},
			initialResponse: &pluginapi.ContainerAllocateResponse{},
			expected:        &pluginapi.ContainerAllocateResponse{},
		},
		{
			description: "enabled MPS populates pipe directory and mounts without CUDA_VISIBLE_DEVICES when no devices allocated",
			opts: &mpsOptions{
				enabled:      true,
				resourceName: spec.ResourceName("nvidia.com/gpu"),
				hostRoot:     mps.Root("/host/mps"),
				daemon: mps.NewDaemon(&rm.ResourceManagerMock{
					ResourceFunc: func() spec.ResourceName {
						return spec.ResourceName("nvidia.com/gpu")
					},
					DevicesFunc: func() rm.Devices {
						return rm.Devices{
							"GPU-1": &rm.Device{
								Device: pluginapi.Device{ID: "GPU-1"},
							},
							"GPU-0": &rm.Device{
								Device: pluginapi.Device{ID: "GPU-0"},
							},
						}
					},
				}, mps.Root("/container/mps")),
			},
			deviceIDs:       nil,
			initialResponse: &pluginapi.ContainerAllocateResponse{},
			expected: &pluginapi.ContainerAllocateResponse{
				Envs: map[string]string{
					"CUDA_MPS_PIPE_DIRECTORY": "/container/mps/nvidia.com/gpu/pipe",
				},
				Mounts: []*pluginapi.Mount{
					{
						ContainerPath: "/container/mps/nvidia.com/gpu/pipe",
						HostPath:      "/host/mps/nvidia.com/gpu/pipe",
					},
					{
						ContainerPath: "/dev/shm",
						HostPath:      "/host/mps/shm",
					},
				},
			},
		},
		{
			description: "enabled MPS scopes CUDA_VISIBLE_DEVICES to specifically allocated device subset",
			opts: &mpsOptions{
				enabled:      true,
				resourceName: spec.ResourceName("nvidia.com/gpu"),
				hostRoot:     mps.Root("/host/mps"),
				daemon: mps.NewDaemon(&rm.ResourceManagerMock{
					ResourceFunc: func() spec.ResourceName {
						return spec.ResourceName("nvidia.com/gpu")
					},
					DevicesFunc: func() rm.Devices {
						return rm.Devices{
							"GPU-1": &rm.Device{
								Device: pluginapi.Device{ID: "GPU-1"},
							},
							"GPU-0": &rm.Device{
								Device: pluginapi.Device{ID: "GPU-0"},
							},
						}
					},
				}, mps.Root("/container/mps")),
			},
			deviceIDs:       []string{"GPU-1"},
			initialResponse: &pluginapi.ContainerAllocateResponse{},
			expected: &pluginapi.ContainerAllocateResponse{
				Envs: map[string]string{
					"CUDA_MPS_PIPE_DIRECTORY": "/container/mps/nvidia.com/gpu/pipe",
					"CUDA_VISIBLE_DEVICES":    "GPU-1",
				},
				Mounts: []*pluginapi.Mount{
					{
						ContainerPath: "/container/mps/nvidia.com/gpu/pipe",
						HostPath:      "/host/mps/nvidia.com/gpu/pipe",
					},
					{
						ContainerPath: "/dev/shm",
						HostPath:      "/host/mps/shm",
					},
				},
			},
		},
		{
			description: "enabled MPS sorts and joins multiple allocated devices",
			opts: &mpsOptions{
				enabled:      true,
				resourceName: spec.ResourceName("nvidia.com/gpu"),
				hostRoot:     mps.Root("/host/mps"),
				daemon: mps.NewDaemon(&rm.ResourceManagerMock{
					ResourceFunc: func() spec.ResourceName {
						return spec.ResourceName("nvidia.com/gpu")
					},
					DevicesFunc: func() rm.Devices {
						return rm.Devices{
							"GPU-1": &rm.Device{
								Device: pluginapi.Device{ID: "GPU-1"},
							},
							"GPU-0": &rm.Device{
								Device: pluginapi.Device{ID: "GPU-0"},
							},
						}
					},
				}, mps.Root("/container/mps")),
			},
			deviceIDs:       []string{"GPU-1", "GPU-0"},
			initialResponse: &pluginapi.ContainerAllocateResponse{},
			expected: &pluginapi.ContainerAllocateResponse{
				Envs: map[string]string{
					"CUDA_MPS_PIPE_DIRECTORY": "/container/mps/nvidia.com/gpu/pipe",
					"CUDA_VISIBLE_DEVICES":    "GPU-0,GPU-1",
				},
				Mounts: []*pluginapi.Mount{
					{
						ContainerPath: "/container/mps/nvidia.com/gpu/pipe",
						HostPath:      "/host/mps/nvidia.com/gpu/pipe",
					},
					{
						ContainerPath: "/dev/shm",
						HostPath:      "/host/mps/shm",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			response := tc.initialResponse
			tc.opts.updateReponse(response, tc.deviceIDs...)
			require.Equal(t, tc.expected, response)
		})
	}
}
