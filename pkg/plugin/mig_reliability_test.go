package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/util"
)

type fakeMIG struct {
	state                           config.MigPartedSpec
	exportErr, applyErr, resolveErr error
	exports, applies, publishes     int
	failPublish                     int
	last                            config.MigPartedSpec
}

func (f *fakeMIG) Export(context.Context) (config.MigPartedSpec, error) {
	f.exports++
	// A hardware export is an independent snapshot, not a reference to plugin edits.
	result := config.MigPartedSpec{Version: f.state.Version, MigConfigs: map[string]config.MigConfigSpecSlice{}}
	for name, entries := range f.state.MigConfigs {
		for _, entry := range entries {
			result.MigConfigs[name] = append(result.MigConfigs[name], deepCopyMigConfig(entry))
		}
	}
	return result, f.exportErr
}
func (*fakeMIG) Device(string) (string, int, error) { return "A100", 0, nil }
func (f *fakeMIG) Resolve(id string, slot int) (string, error) {
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	return "MIG-slot-" + string(rune('0'+slot)), nil
}
func (f *fakeMIG) Apply(_ context.Context, s config.MigPartedSpec) error {
	f.applies++
	f.last = s
	// A failed apply may have changed hardware; retry must discover, not roll back.
	f.state = s
	return f.applyErr
}
func (f *fakeMIG) Publish([]string) error {
	f.publishes++
	if f.publishes <= f.failPublish {
		return errors.New("CDI unavailable")
	}
	return nil
}

func migFixture(t *testing.T, counts map[string]int32) (*nvidiaDevicePlugin, *fakeMIG) {
	t.Helper()
	saved := config.SchedulerConfig
	t.Cleanup(func() { config.SchedulerConfig = saved })
	config.SchedulerConfig.MigGeometriesList = []config.AllowedMigGeometries{{Models: []string{"A100"}, Geometries: []config.Geometry{{Group: "group2", Instances: []config.MigTemplate{{Name: "1g.5gb", Count: 2}}}}}}
	f := &fakeMIG{state: config.MigPartedSpec{Version: "v1", MigConfigs: map[string]config.MigConfigSpecSlice{"current": {{Devices: []int32{0}, MigEnabled: true, MigDevices: counts}}}}}
	return &nvidiaDevicePlugin{ctx: context.Background(), migBackend: f}, f
}

func TestMIGPreservesAssignedGeometryAndSlotAfterRestart(t *testing.T) {
	plugin, f := migFixture(t, map[string]int32{"1g.5gb": 2})
	req := util.ContainerDevices{{UUID: "GPU-test[group2-1]"}}
	ids, err := plugin.GetContainerDeviceStrArray(req)
	require.NoError(t, err)
	require.Equal(t, []string{"MIG-slot-1"}, ids)
	require.Zero(t, f.applies)
	restarted := &nvidiaDevicePlugin{ctx: context.Background(), migBackend: f}
	again, err := restarted.GetContainerDeviceStrArray(req)
	require.NoError(t, err)
	require.Equal(t, ids, again)
	require.Zero(t, f.applies)
	require.Equal(t, 2, f.publishes, "CDI must be checked even when geometry is unchanged")
}

func TestMIGAppliesOnlyEmptyAssignedGPU(t *testing.T) {
	plugin, f := migFixture(t, nil)
	f.state.MigConfigs["current"] = append(f.state.MigConfigs["current"], config.MigConfigSpec{Devices: []int32{1}, MigEnabled: true, MigDevices: map[string]int32{"7g.40gb": 1}})
	ids, err := plugin.GetContainerDeviceStrArray(util.ContainerDevices{{UUID: "GPU-test[group2-0]"}})
	require.NoError(t, err)
	require.Equal(t, []string{"MIG-slot-0"}, ids)
	require.Equal(t, 1, f.applies)
	require.Len(t, f.last.MigConfigs["current"], 1)
	require.Equal(t, []int32{0}, f.last.MigConfigs["current"][0].Devices)
}

func TestMIGRejectsConflictsAndInvalidAssignments(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		counts   map[string]int32
	}{
		{"existing geometry", "GPU-test[group2-0]", map[string]int32{"7g.40gb": 1}},
		{"extra profile", "GPU-test[group2-0]", map[string]int32{"1g.5gb": 2, "2g.10gb": 1}},
		{"negative slot", "GPU-test[group2--1]", nil},
		{"out of range", "GPU-test[group2-2]", nil},
		{"unknown group", "GPU-test[missing-0]", nil},
		{"trailing data", "GPU-test[group2-0]junk", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plugin, f := migFixture(t, tc.counts)
			_, err := plugin.GetContainerDeviceStrArray(util.ContainerDevices{{UUID: tc.id}})
			require.Error(t, err)
			require.Zero(t, f.applies)
			require.Zero(t, f.publishes)
		})
	}
}

func TestMIGPartialApplyFailureRefreshesOnRetry(t *testing.T) {
	plugin, f := migFixture(t, nil)
	f.applyErr = errors.New("apply failed")
	req := util.ContainerDevices{{UUID: "GPU-test[group2-0]"}}
	_, err := plugin.GetContainerDeviceStrArray(req)
	require.ErrorContains(t, err, "apply failed")
	require.Zero(t, f.publishes)
	f.applyErr = nil
	_, err = plugin.GetContainerDeviceStrArray(req)
	require.NoError(t, err)
	require.Equal(t, 2, f.exports)
	require.Equal(t, 1, f.applies, "must not reset instances created before failed apply")
}

func TestMIGRetriesCDIWithoutReapplying(t *testing.T) {
	plugin, f := migFixture(t, nil)
	f.failPublish = 1
	_, err := plugin.GetContainerDeviceStrArray(util.ContainerDevices{{UUID: "GPU-test[group2-0]"}})
	require.NoError(t, err)
	require.Equal(t, 1, f.applies)
	require.Equal(t, 2, f.publishes)
}

func TestMIGReturnsExportResolutionAndCDIErrors(t *testing.T) {
	for _, kind := range []string{"export", "resolve", "CDI"} {
		t.Run(kind, func(t *testing.T) {
			plugin, f := migFixture(t, map[string]int32{"1g.5gb": 2})
			switch kind {
			case "export":
				f.exportErr = errors.New("export failure")
			case "resolve":
				f.resolveErr = errors.New("NVML failure")
			case "CDI":
				f.failPublish = 10
			}
			_, err := plugin.GetContainerDeviceStrArray(util.ContainerDevices{{UUID: "GPU-test[group2-0]"}})
			require.Error(t, err)
			require.Zero(t, f.applies)
		})
	}
}

func TestWriteMIGConfigFailureAndPrivatePermissions(t *testing.T) {
	desired := config.MigPartedSpec{Version: "v1"}
	require.ErrorContains(t, writeMIGConfig(filepath.Join(t.TempDir(), "missing", "config"), desired), "write MIG config")
	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, writeMIGConfig(path, desired))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestProcessMIGConfigsRejectsAmbiguousState(t *testing.T) {
	plugin := &nvidiaDevicePlugin{}
	for _, entries := range []config.MigConfigSpecSlice{
		nil,
		{{Devices: []int32{0}}, {Devices: []int32{0}}},
		{{Devices: []int32{2}}},
		{{Devices: []int32{0}, DeviceFilter: "unsupported"}},
	} {
		_, err := plugin.processMigConfigs(map[string]config.MigConfigSpecSlice{"current": entries}, 1)
		require.Error(t, err)
	}
	entries, err := plugin.processMigConfigs(map[string]config.MigConfigSpecSlice{"current": {{MigEnabled: true, MigDevices: map[string]int32{"1g.5gb": 2}}}}, 2)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	entries[0].MigDevices["1g.5gb"] = 1
	require.Equal(t, int32(2), entries[1].MigDevices["1g.5gb"], "per-GPU maps must be independent")
}

func TestDecodeMIGPartedAllAndExplicitDevices(t *testing.T) {
	for _, selection := range []string{"all", "[0, 1]"} {
		raw, err := decodeMIGExport([]byte("version: v1\nmig-configs:\n  current:\n  - devices: " + selection + "\n    mig-enabled: true\n    mig-devices: {1g.5gb: 2}\n"))
		require.NoError(t, err)
		normalized, err := (&nvidiaDevicePlugin{}).processMigConfigs(raw.MigConfigs, 2)
		require.NoError(t, err)
		require.Len(t, normalized, 2)
		require.Equal(t, []int32{1}, normalized[1].Devices)
	}
	for _, selection := range []string{"invalid", "[]", "[-1]", "[x]", "null"} {
		_, err := decodeMIGExport([]byte("version: v1\nmig-configs:\n  current:\n  - devices: " + selection + "\n"))
		require.Error(t, err)
	}
}
