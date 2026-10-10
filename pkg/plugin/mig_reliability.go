package plugin

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"gopkg.in/yaml.v2"
	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/util"
)

type migBackend interface {
	Export(context.Context) (config.MigPartedSpec, error)
	Device(string) (string, int, error)
	Resolve(string, int) (string, error)
	Apply(context.Context, config.MigPartedSpec) error
	Publish([]string) error
}

type hardwareMIG struct{ plugin *nvidiaDevicePlugin }

func (plugin *nvidiaDevicePlugin) migOperations() migBackend {
	if plugin.migBackend != nil {
		return plugin.migBackend
	}
	return &hardwareMIG{plugin: plugin}
}

func runMIGCommand(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "nvidia-mig-parted", args...)
	out, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-mig-parted %v: %w", args, err)
	}
	return out, nil
}

func (h *hardwareMIG) Export(ctx context.Context) (config.MigPartedSpec, error) {
	var current config.MigPartedSpec
	data, err := runMIGCommand(ctx, "export")
	if err != nil {
		return current, err
	}
	if err := yaml.UnmarshalStrict(data, &current); err != nil {
		return current, fmt.Errorf("parse MIG export: %w", err)
	}
	count, err := util.GetDeviceNums()
	if err != nil {
		return current, err
	}
	normalized, err := h.plugin.processMigConfigs(current.MigConfigs, count)
	if err != nil {
		return current, err
	}
	if current.Version != "v1" {
		return current, fmt.Errorf("unsupported MIG config version %q", current.Version)
	}
	current.MigConfigs = map[string]config.MigConfigSpecSlice{"current": normalized}
	return current, nil
}

func (*hardwareMIG) Device(id string) (string, int, error) { return util.GetIndexAndTypeFromUUID(id) }
func (*hardwareMIG) Resolve(id string, slot int) (string, error) {
	return util.GetMigUUIDFromIndex(id, slot)
}

func (*hardwareMIG) Apply(ctx context.Context, desired config.MigPartedSpec) error {
	// Only empty, idle physical GPUs can be configured automatically. A conflict
	// with an existing layout requires operator coordination, never destruction.
	for _, entry := range desired.MigConfigs["current"] {
		for _, index := range entry.Devices {
			dev, ret := config.Nvml().DeviceGetHandleByIndex(int(index))
			if ret != nvml.SUCCESS {
				return fmt.Errorf("get GPU %d before apply: %v", index, ret)
			}
			maxMIG, ret := dev.GetMaxMigDeviceCount()
			if ret != nvml.SUCCESS && ret != nvml.ERROR_NOT_SUPPORTED {
				return fmt.Errorf("inspect MIG instances on GPU %d: %v", index, ret)
			}
			for slot := 0; slot < maxMIG; slot++ {
				_, ret := dev.GetMigDeviceHandleByIndex(slot)
				if ret == nvml.SUCCESS {
					return fmt.Errorf("GPU %d gained MIG instances before apply; refusing layout change", index)
				}
				if ret != nvml.ERROR_NOT_FOUND && ret != nvml.ERROR_INVALID_ARGUMENT {
					return fmt.Errorf("inspect MIG slot %d: %v", slot, ret)
				}
			}
			compute, ret := dev.GetComputeRunningProcesses()
			if ret != nvml.SUCCESS {
				return fmt.Errorf("check GPU %d compute processes: %v", index, ret)
			}
			graphics, ret := dev.GetGraphicsRunningProcesses()
			if ret != nvml.SUCCESS {
				return fmt.Errorf("check GPU %d graphics processes: %v", index, ret)
			}
			if len(compute)+len(graphics) > 0 {
				return fmt.Errorf("GPU %d is in use; refusing MIG layout change", index)
			}
		}
	}
	dir, err := os.MkdirTemp("", "volcano-mig-")
	if err != nil {
		return fmt.Errorf("create MIG config directory: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.yaml")
	if err := writeMIGConfig(path, desired); err != nil {
		return err
	}
	_, err = runMIGCommand(ctx, "apply", "-f", path, "-c", "current")
	return err
}

func writeMIGConfig(path string, desired config.MigPartedSpec) error {
	data, err := yaml.Marshal(desired)
	if err != nil {
		return fmt.Errorf("marshal MIG config: %w", err)
	}
	// The private directory and unique filename avoid partially overwriting the
	// configuration of another Allocate call or a previous plugin process.
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write MIG config: %w", err)
	}
	return nil
}

func (h *hardwareMIG) Publish(ids []string) error {
	if !h.plugin.deviceListStrategies.AnyCDIEnabled() {
		return nil
	}
	if err := h.plugin.cdiHandler.CreateSpecFile(); err != nil {
		return fmt.Errorf("publish CDI: %w", err)
	}
	cache := cdiapi.GetDefaultCache()
	if err := cache.Refresh(); err != nil {
		return fmt.Errorf("refresh CDI: %w", err)
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, h.plugin.cdiHandler.QualifiedName("gpu", id))
	}
	return validateMIGCDIDevices(cache, names)
}

func validateMIGCDIDevices(cache *cdiapi.Cache, names []string) error {
	for _, name := range names {
		device := cache.GetDevice(name)
		if device == nil {
			return fmt.Errorf("requested MIG CDI device %s is missing", name)
		}
		hasCapability := false
		for _, node := range device.ContainerEdits.DeviceNodes {
			if node != nil && strings.Contains(node.Path, "nvidia-cap") {
				hasCapability = true
			}
		}
		if !hasCapability {
			return fmt.Errorf("requested MIG CDI device %s has no capability device", name)
		}
	}
	return nil
}

// GetContainerDeviceStrArray executes the exact geometry and slot assigned by
// Volcano. It neither chooses another slot nor replaces a nonempty layout.
func (plugin *nvidiaDevicePlugin) GetContainerDeviceStrArray(devices util.ContainerDevices) ([]string, error) {
	result := make([]string, 0, len(devices))
	hasMIG := false
	for _, device := range devices {
		hasMIG = hasMIG || strings.ContainsAny(device.UUID, "[]")
	}
	if !hasMIG {
		for _, device := range devices {
			result = append(result, device.UUID)
		}
		return result, nil
	}
	if plugin.config != nil && plugin.config.Flags.Plugin.DeviceIDStrategy != nil && *plugin.config.Flags.Plugin.DeviceIDStrategy != spec.DeviceIDStrategyUUID {
		return nil, fmt.Errorf("MIG geometry allocation requires uuid device ID strategy")
	}
	plugin.migMutex.Lock()
	defer plugin.migMutex.Unlock()
	backend := plugin.migOperations()
	ctx := plugin.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	current, err := backend.Export(ctx)
	if err != nil {
		return nil, fmt.Errorf("refresh MIG state: %w", err)
	}
	plugin.migCurrent = current
	desired := config.MigPartedSpec{Version: current.Version, MigConfigs: map[string]config.MigConfigSpecSlice{"current": {}}}
	type allocation struct {
		id   string
		slot int
	}
	var allocations []allocation
	for _, device := range devices {
		if !strings.ContainsAny(device.UUID, "[]") {
			return nil, fmt.Errorf("mixed physical and MIG geometry requests are not supported")
		}
		if _, _, err := util.ExtractMigTemplatesFromUUID(device.UUID); err != nil {
			return nil, err
		}
		model, index, err := backend.Device(device.UUID)
		if err != nil {
			return nil, err
		}
		slot, change, err := plugin.GenerateMigTemplate(model, index, device)
		if err != nil {
			return nil, err
		}
		if change {
			// GenerateMigTemplate changed only this GPU in our private snapshot.
			for _, entry := range plugin.migCurrent.MigConfigs["current"] {
				if containsDevice(index, entry.Devices) {
					desired.MigConfigs["current"] = append(desired.MigConfigs["current"], deepCopyMigConfig(entry))
				}
			}
		}
		allocations = append(allocations, allocation{device.UUID, slot})
	}
	if len(desired.MigConfigs["current"]) > 0 {
		if err := backend.Apply(ctx, desired); err != nil {
			return nil, fmt.Errorf("apply assigned MIG geometry: %w", err)
		}
	}
	// Hardware discovery and CDI publication can lag after apply. A failed
	// request always re-exports hardware next time, even if apply partly succeeded.
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		result = result[:0]
		lastErr = nil
		for _, allocation := range allocations {
			id, err := backend.Resolve(allocation.id, allocation.slot)
			if err != nil {
				lastErr = err
				break
			}
			if id == "" {
				lastErr = fmt.Errorf("empty MIG UUID for %s", allocation.id)
				break
			}
			result = append(result, id)
		}
		if lastErr == nil {
			lastErr = backend.Publish(result)
		}
		if lastErr == nil {
			return result, nil
		}
		if attempt < 4 {
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, fmt.Errorf("MIG device/CDI not ready after 5 attempts: %w", lastErr)
}

func (plugin *nvidiaDevicePlugin) GenerateMigTemplate(model string, index int, device util.ContainerDevice) (int, bool, error) {
	group, slot, err := util.ExtractMigTemplatesFromUUID(device.UUID)
	if err != nil {
		return -1, false, err
	}
	for _, templates := range config.SchedulerConfig.MigGeometriesList {
		if !containsModel(model, templates.Models) {
			continue
		}
		for _, geometry := range templates.Geometries {
			if geometry.Group != group {
				continue
			}
			counts := map[string]int32{}
			total := int64(0)
			for _, instance := range geometry.Instances {
				if instance.Name == "" || instance.Count <= 0 {
					return -1, false, fmt.Errorf("invalid MIG geometry %s", group)
				}
				if _, exists := counts[instance.Name]; exists {
					return -1, false, fmt.Errorf("duplicate MIG profile %s", instance.Name)
				}
				counts[instance.Name] = instance.Count
				total += int64(instance.Count)
			}
			if int64(slot) >= total {
				return -1, false, fmt.Errorf("MIG slot %d outside geometry %s (%d slots)", slot, group, total)
			}
			for i, current := range plugin.migCurrent.MigConfigs["current"] {
				if !containsDevice(index, current.Devices) {
					continue
				}
				if current.MigEnabled && maps.Equal(current.MigDevices, counts) {
					return slot, false, nil
				}
				for _, count := range current.MigDevices {
					if count > 0 {
						return -1, false, fmt.Errorf("GPU %d has a different nonempty MIG layout; refusing to replace existing instances", index)
					}
				}
				replacement := deepCopyMigConfig(current)
				replacement.MigEnabled = true
				replacement.MigDevices = counts
				plugin.migCurrent.MigConfigs["current"][i] = replacement
				return slot, true, nil
			}
			return -1, false, fmt.Errorf("GPU %d missing from MIG export", index)
		}
		return -1, false, fmt.Errorf("MIG geometry %s not found for %s", group, model)
	}
	return -1, false, fmt.Errorf("no MIG geometry model matches %s", model)
}
