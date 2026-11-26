// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"testing"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/rm"
	"volcano.sh/k8s-device-plugin/pkg/util"
)

type fixtureBackend string

func (b fixtureBackend) Mode() string { return string(b) }
func (b fixtureBackend) Ready(host HostCapabilities) bool {
	return host.VidmemCapacityBytes["pci-fixture"] > 0
}
func (b fixtureBackend) Allocate(_ context.Context, _ *v1.Pod, _ v1.Container, _ util.ContainerDevices, response *pluginapi.ContainerAllocateResponse) error {
	response.Envs["TEST_ALLOCATOR"] = b.Mode()
	return nil
}

func TestSelectBackend(t *testing.T) {
	host := HostCapabilities{VidmemCapacityBytes: map[string]uint64{"pci-fixture": 1024}}
	backends := []AllocationBackend{fixtureBackend("first"), fixtureBackend("second")}
	for _, tc := range []struct {
		name, pin, want string
		host            HostCapabilities
		probeOK         bool
		count           int
	}{
		{"explicit core", "hami-core", "hami-core", host, true, 1},
		{"explicit MIG", "mig", "mig", host, false, 0},
		{"probe only", "", "hami-core", host, true, 0},
		{"missing capability", "", "hami-core", HostCapabilities{}, true, 1},
		{"failed probe", "", "hami-core", host, false, 1},
		{"one ready", "", "first", host, true, 1},
		{"no priority defined", "", "hami-core", host, true, 2},
		{"explicit candidate", "second", "second", host, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mode, handler, err := SelectBackend(tc.pin, tc.host, tc.probeOK, backends[:tc.count]...)
			require.NoError(t, err)
			require.Equal(t, tc.want, mode)
			if mode == "mig" || mode == "hami-core" {
				require.Nil(t, handler)
			} else {
				require.Equal(t, mode, handler.Mode())
			}
		})
	}
	_, _, err := SelectBackend("first", host, false, backends...)
	require.Error(t, err)
}

type registrationNVML struct{ mockNvmlWithDevice }
type registrationDevice struct{ mockDeviceSuccess }

func (registrationNVML) DeviceGetHandleByUUID(string) (nvml.Device, nvml.Return) {
	return registrationDevice{}, nvml.SUCCESS
}
func (registrationNVML) DeviceGetMemoryInfo(nvml.Device) (nvml.Memory, nvml.Return) {
	return nvml.Memory{Total: 1024 * 1024}, nvml.SUCCESS
}
func (registrationNVML) DeviceGetName(nvml.Device) (string, nvml.Return) {
	return "fixture", nvml.SUCCESS
}
func (registrationDevice) GetMinorNumber() (int, nvml.Return) { return 0, nvml.SUCCESS }

func TestSelectedBackendRegistersAndAllocates(t *testing.T) {
	for _, tc := range []struct {
		name, pin, want string
		probeOK         bool
	}{
		{"ready allocator", "", "test-backend", true},
		{"explicit core", "hami-core", "hami-core", true},
		{"explicit MIG", "mig", "mig", true},
		{"probe failure", "", "hami-core", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOOK_PATH", t.TempDir())
			kube := newAllocateFailureClient(t, "GPU-1234,NVIDIA,1000,10:")
			config.SetNvml(registrationNVML{})
			t.Cleanup(config.ResetNvml)
			oldNode, oldMode, oldFactor := *nodeName, config.Mode, config.GPUMemoryFactor
			t.Cleanup(func() { *nodeName, config.Mode, config.GPUMemoryFactor = oldNode, oldMode, oldFactor })
			*nodeName, config.GPUMemoryFactor = "test-node", 1
			mode, handler, err := SelectBackend(tc.pin, HostCapabilities{VidmemCapacityBytes: map[string]uint64{"pci-fixture": 1024}}, tc.probeOK, fixtureBackend("test-backend"))
			require.NoError(t, err)
			config.Mode = mode
			require.NoError(t, RegisterInAnnotation([]*pluginapi.Device{{ID: "GPU-1234", Health: pluginapi.Healthy}}))
			node, err := kube.CoreV1().Nodes().Get(t.Context(), *nodeName, metav1.GetOptions{})
			require.NoError(t, err)
			require.Contains(t, node.Annotations[util.NodeNvidiaDeviceRegistered], ","+tc.want+":")

			strategies, err := spec.NewDeviceListStrategies([]string{spec.DeviceListStrategyEnvVar})
			require.NoError(t, err)
			p := nvidiaDevicePlugin{
				backend:              handler,
				rm:                   &rm.ResourceManagerMock{ResourceFunc: func() spec.ResourceName { return spec.ResourceName(util.ResourceName) }},
				config:               &spec.Config{Flags: spec.Flags{CommandLineFlags: spec.CommandLineFlags{Plugin: &spec.PluginCommandLineFlags{DeviceIDStrategy: ptr(spec.DeviceIDStrategyUUID)}}}},
				deviceListStrategies: strategies,
			}
			resp, err := p.Allocate(t.Context(), &pluginapi.AllocateRequest{ContainerRequests: []*pluginapi.ContainerAllocateRequest{{DevicesIds: []string{"test-gpu"}}}})
			require.NoError(t, err)
			require.Len(t, resp.ContainerResponses, 1)
			env := resp.ContainerResponses[0].Envs
			switch tc.want {
			case "hami-core":
				require.Equal(t, "1000m", env["CUDA_DEVICE_MEMORY_LIMIT_0"])
				require.NotContains(t, env, "TEST_ALLOCATOR")
			case "mig":
				require.NotContains(t, env, "CUDA_DEVICE_MEMORY_LIMIT_0")
				require.NotContains(t, env, "TEST_ALLOCATOR")
			default:
				require.Equal(t, tc.want, env["TEST_ALLOCATOR"])
				require.NotContains(t, env, "CUDA_DEVICE_MEMORY_LIMIT_0")
			}
		})
	}
}
