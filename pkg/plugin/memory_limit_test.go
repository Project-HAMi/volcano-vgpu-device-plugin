/*
Copyright 2026 The HAMi Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"volcano.sh/k8s-device-plugin/pkg/rm"
	"volcano.sh/k8s-device-plugin/pkg/util"
)

func TestMemoryLimitEnvs(t *testing.T) {
	// Host index 0 is GPU-b and index 1 is GPU-a, as on a node where NVML
	// enumerates GPU-b first by PCI bus ID.
	devices := rm.Devices{
		"GPU-a": {Device: pluginapi.Device{ID: "GPU-a"}, Index: "1"},
		"GPU-b": {Device: pluginapi.Device{ID: "GPU-b"}, Index: "0"},
	}
	for _, tc := range []struct {
		name string
		devs util.ContainerDevices
		want map[string]string
	}{
		{
			name: "annotation order differs from host index order",
			devs: util.ContainerDevices{{UUID: "GPU-a", Usedmem: 1000}, {UUID: "GPU-b", Usedmem: 200}},
			want: map[string]string{"CUDA_DEVICE_MEMORY_LIMIT_0": "20000m", "CUDA_DEVICE_MEMORY_LIMIT_1": "100000m"},
		},
		{
			name: "annotation order matches host index order",
			devs: util.ContainerDevices{{UUID: "GPU-b", Usedmem: 200}, {UUID: "GPU-a", Usedmem: 1000}},
			want: map[string]string{"CUDA_DEVICE_MEMORY_LIMIT_0": "20000m", "CUDA_DEVICE_MEMORY_LIMIT_1": "100000m"},
		},
		{
			name: "unknown device keeps annotation order",
			devs: util.ContainerDevices{{UUID: "GPU-a", Usedmem: 1000}, {UUID: "GPU-x", Usedmem: 200}},
			want: map[string]string{"CUDA_DEVICE_MEMORY_LIMIT_0": "100000m", "CUDA_DEVICE_MEMORY_LIMIT_1": "20000m"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := append(util.ContainerDevices(nil), tc.devs...)
			require.Equal(t, tc.want, memoryLimitEnvs(tc.devs, devices, 100))
			require.Equal(t, before, tc.devs, "input must not be reordered")
		})
	}
}
