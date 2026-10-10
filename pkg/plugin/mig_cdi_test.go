package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
	specs "tags.cncf.io/container-device-interface/specs-go"
)

func TestMIGCDIRequiresExactRequestedDeviceAndCapabilities(t *testing.T) {
	cache, err := cdiapi.NewCache(cdiapi.WithSpecDirs(t.TempDir()), cdiapi.WithAutoRefresh(false))
	require.NoError(t, err)
	names := []string{"k8s.device-plugin.nvidia.com/gpu=MIG-wanted"}
	require.ErrorContains(t, validateMIGCDIDevices(cache, names), "missing")
	spec := &specs.Spec{Version: "0.6.0", Kind: "k8s.device-plugin.nvidia.com/gpu", Devices: []specs.Device{{Name: "MIG-wanted", ContainerEdits: specs.ContainerEdits{DeviceNodes: []*specs.DeviceNode{{Path: "/dev/nvidia0"}}}}}}
	require.NoError(t, cache.WriteSpec(spec, "gpu.json"))
	require.NoError(t, cache.Refresh())
	require.ErrorContains(t, validateMIGCDIDevices(cache, names), "capability")
	spec.Devices[0].ContainerEdits.DeviceNodes = append(spec.Devices[0].ContainerEdits.DeviceNodes, &specs.DeviceNode{Path: "/dev/nvidia-caps/nvidia-cap1"})
	require.NoError(t, cache.WriteSpec(spec, "gpu.json"))
	require.NoError(t, cache.Refresh())
	require.NoError(t, validateMIGCDIDevices(cache, names))
	require.ErrorContains(t, validateMIGCDIDevices(cache, []string{"k8s.device-plugin.nvidia.com/gpu=MIG-other"}), "missing")
}
