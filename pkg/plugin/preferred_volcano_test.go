/*
Copyright 2026 The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package plugin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/rm"
	"volcano.sh/k8s-device-plugin/pkg/util"
)

func TestPreferredAllocationIsNotAdvertised(t *testing.T) {
	plugin := &nvidiaDevicePlugin{}
	options, err := plugin.GetDevicePluginOptions(context.Background(), &pluginapi.Empty{})
	require.NoError(t, err)
	require.False(t, options.GetPreferredAllocationAvailable)
	response, err := plugin.GetPreferredAllocation(context.Background(), &pluginapi.PreferredAllocationRequest{})
	require.Nil(t, response)
	require.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestVolcanoGPUAndLimitOrder(t *testing.T) {
	plugin := &nvidiaDevicePlugin{
		config: &spec.Config{Flags: spec.Flags{CommandLineFlags: spec.CommandLineFlags{
			Plugin: &spec.PluginCommandLineFlags{DeviceIDStrategy: ptr(spec.DeviceIDStrategyIndex)},
		}}},
		rm: &rm.ResourceManagerMock{DevicesFunc: func() rm.Devices {
			return rm.Devices{
				"GPU-A::0": {Device: pluginapi.Device{ID: "GPU-A::0"}, Index: "0"},
				"GPU-B::0": {Device: pluginapi.Device{ID: "GPU-B::0"}, Index: "1"},
			}
		}},
		deviceListStrategies: spec.DeviceListStrategies{"envvar": true},
	}
	devices := util.ContainerDevices{
		{UUID: "GPU-B", Usedmem: 2000, Usedcores: 30},
		{UUID: "GPU-A", Usedmem: 1000, Usedcores: 30},
	}
	require.NoError(t, validateVolcanoDeviceLimits(devices))
	previousFactor := config.GPUMemoryFactor
	config.GPUMemoryFactor = 1
	t.Cleanup(func() { config.GPUMemoryFactor = previousFactor })
	for i := 0; i < 20; i++ {
		response, err := plugin.getAllocateResponse([]string{"GPU-B", "GPU-A"})
		require.NoError(t, err)
		applyVolcanoDeviceLimits(response, devices)
		require.Equal(t, "1,0", response.Envs[deviceListEnvVar])
		require.Equal(t, "2000m", response.Envs["CUDA_DEVICE_MEMORY_LIMIT_0"])
		require.Equal(t, "1000m", response.Envs["CUDA_DEVICE_MEMORY_LIMIT_1"])
		require.Equal(t, "30", response.Envs["CUDA_DEVICE_SM_LIMIT"])
	}
	_, err := plugin.getAllocateResponse([]string{"GPU-C"})
	require.ErrorContains(t, err, "not registered")
}

func TestVolcanoRejectsUnpairableLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		devices util.ContainerDevices
		want    string
	}{
		{"no GPU", nil, "no GPUs"},
		{"duplicate GPU", util.ContainerDevices{{UUID: "GPU-A", Usedcores: 30}, {UUID: "GPU-A", Usedcores: 30}}, "more than once"},
		{"different cores", util.ContainerDevices{{UUID: "GPU-A", Usedcores: 30}, {UUID: "GPU-B", Usedcores: 20}}, "different GPU core limits"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, validateVolcanoDeviceLimits(tc.devices), tc.want)
		})
	}
}
