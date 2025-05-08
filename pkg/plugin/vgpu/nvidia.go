/*
Copyright 2023 The Volcano Authors.

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

package vgpu

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"k8s.io/klog"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"volcano.sh/k8s-device-plugin/pkg/plugin/vgpu/config"
	"volcano.sh/k8s-device-plugin/pkg/plugin/vgpu/util"
)

const (
	envDisableHealthChecks = "DP_DISABLE_HEALTHCHECKS"
	allHealthChecks        = "xids"
)

// Device couples an underlying pluginapi.Device type with its device node paths
type Device struct {
	pluginapi.Device
	Paths  []string
	Index  string
	Memory uint64
}

// ResourceManager provides an interface for listing a set of Devices and checking health on them
type ResourceManager interface {
	Devices() []*Device
	CheckHealth(stop <-chan interface{}, devices []*Device, unhealthy chan<- *Device)
}

// GpuDeviceManager implements the ResourceManager interface for full GPU devices
type GpuDeviceManager struct {
	skipMigEnabledGPUs bool
}

// MigDeviceManager implements the ResourceManager interface for MIG devices
type MigDeviceManager struct {
	strategy MigStrategy
	resource string
}

func check(ret nvml.Return) {
	if ret != nvml.SUCCESS {
		log.Panicln("Fatal:", ret)
	}
}

// NewGpuDeviceManager returns a reference to a new GpuDeviceManager
func NewGpuDeviceManager(skipMigEnabledGPUs bool) *GpuDeviceManager {
	return &GpuDeviceManager{
		skipMigEnabledGPUs: skipMigEnabledGPUs,
	}
}

// NewMigDeviceManager returns a reference to a new MigDeviceManager
func NewMigDeviceManager(strategy MigStrategy, resource string) *MigDeviceManager {
	return &MigDeviceManager{
		strategy: strategy,
		resource: resource,
	}
}

// Devices returns a list of devices from the GpuDeviceManager
func (g *GpuDeviceManager) Devices() []*Device {
	n, ret := config.Nvml().DeviceGetCount()
	check(ret)
	if n > util.DeviceLimit {
		n = util.DeviceLimit
	}

	var devs []*Device
	for i := 0; i < n; i++ {
		d, ret := config.Nvml().DeviceGetHandleByIndex(i)
		check(ret)

		migMode, _, ret := d.GetMigMode()
		if ret != nvml.SUCCESS {
			if ret == nvml.ERROR_NOT_SUPPORTED {
				migMode = nvml.DEVICE_MIG_DISABLE
			} else {
				check(ret)
			}
		}

		if migMode == nvml.DEVICE_MIG_ENABLE && g.skipMigEnabledGPUs {
			continue
		}

		// Auto ebale MIG mode when the plugin is running in MIG mode
		if config.Mode == "mig" && migMode != nvml.DEVICE_MIG_ENABLE {
			if ret == nvml.ERROR_NOT_SUPPORTED {
				klog.V(4).Infof("Node is configed as MIG mode, but GPU %v does not support MIG mode", i)
				continue
			}
			ret, stat := d.SetMigMode(nvml.DEVICE_MIG_ENABLE)
			if ret != nvml.SUCCESS || stat != nvml.SUCCESS {
				klog.V(4).Infof("Node is configed as MIG mode, but failed to enable MIG mode for GPU %v : ret=%v, stat=%v", i, ret, stat)
				continue
			}
		}

		dev, err := buildDevice(fmt.Sprintf("%v", i), d)
		if err != nil {
			log.Panicln("Fatal:", err)
		}

		devs = append(devs, dev)
	}

	return devs
}

// Devices returns a list of devices from the MigDeviceManager
func (m *MigDeviceManager) Devices() []*Device {
	n, ret := config.Nvml().DeviceGetCount()
	check(ret)
	if n > util.DeviceLimit {
		n = util.DeviceLimit
	}

	var devs []*Device
	for i := 0; i < n; i++ {
		d, ret := config.Nvml().DeviceGetHandleByIndex(i)
		check(ret)

		migMode, _, ret := d.GetMigMode()
		if ret != nvml.SUCCESS {
			if ret == nvml.ERROR_NOT_SUPPORTED {
				migMode = nvml.DEVICE_MIG_DISABLE
			} else {
				check(ret)
			}
		}

		if migMode != nvml.DEVICE_MIG_ENABLE {
			continue
		}

		err := config.Device().VisitMigDevices(func(i int, d device.Device, j int, mig device.MigDevice) error {
			dev, err := buildMigDevice(fmt.Sprintf("%v:%v", i, j), mig)
			if err != nil {
				log.Panicln("Fatal:", err)
			}
			devs = append(devs, dev)
			return nil
		})
		if err != nil {
			log.Fatalf("VisitMigDevices error: %v", err)
		}
	}

	return devs
}

// CheckHealth performs health checks on a set of devices, writing to the 'unhealthy' channel with any unhealthy devices
func (g *GpuDeviceManager) CheckHealth(stop <-chan interface{}, devices []*Device, unhealthy chan<- *Device) {
	checkHealth(stop, devices, unhealthy)
}

// CheckHealth performs health checks on a set of devices, writing to the 'unhealthy' channel with any unhealthy devices
func (m *MigDeviceManager) CheckHealth(stop <-chan interface{}, devices []*Device, unhealthy chan<- *Device) {
	checkHealth(stop, devices, unhealthy)
}

func buildDevice(index string, d nvml.Device) (*Device, error) {
	uuid, ret := config.Nvml().DeviceGetUUID(d)
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting UUID of device: %v", ret)
	}

	minor, ret := config.Nvml().DeviceGetMinorNumber(d)
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting minor number of device: %v", ret)
	}
	paths := []string{fmt.Sprintf("/dev/nvidia%d", minor)}

	memory, ret := config.Nvml().DeviceGetMemoryInfo(d)
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting memory info of device: %v", ret)
	}

	hasNuma, numa, err := getNumaNode(d)
	if err != nil {
		return nil, fmt.Errorf("error getting device NUMA node: %v", err)
	}

	dev := Device{}
	dev.ID = uuid
	dev.Health = pluginapi.Healthy
	dev.Paths = paths
	dev.Index = index
	dev.Memory = memory.Total / (1024 * 1024)
	if hasNuma {
		dev.Topology = &pluginapi.TopologyInfo{
			Nodes: []*pluginapi.NUMANode{
				{
					ID: int64(numa),
				},
			},
		}
	}
	return &dev, nil
}

func buildMigDevice(index string, d device.MigDevice) (*Device, error) {
	uuid, ret := config.Nvml().DeviceGetUUID(d)
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting UUID of device: %v", ret)
	}

	paths, err := getMigPaths(d)
	if err != nil {
		return nil, fmt.Errorf("error getting MIG paths of device: %v", err)
	}

	memory, ret := config.Nvml().DeviceGetMemoryInfo(d)
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting memory info of device: %v", ret)
	}

	parent, ret := d.GetDeviceHandleFromMigDeviceHandle()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting parent GPU device from MIG device: %v", ret)
	}
	hasNuma, numa, err := getNumaNode(parent)
	if err != nil {
		return nil, fmt.Errorf("error getting device NUMA node: %v", err)
	}

	dev := Device{}
	dev.ID = uuid
	dev.Health = pluginapi.Healthy
	dev.Paths = paths
	dev.Index = index
	dev.Memory = memory.Total / (1024 * 1024)
	if hasNuma {
		dev.Topology = &pluginapi.TopologyInfo{
			Nodes: []*pluginapi.NUMANode{
				{
					ID: int64(numa),
				},
			},
		}
	}
	return &dev, nil
}

func getMigPaths(d device.MigDevice) ([]string, error) {
	capDevicePaths, err := GetMigCapabilityDevicePaths()
	if err != nil {
		return nil, fmt.Errorf("error getting MIG capability device paths: %v", err)
	}

	gi, ret := d.GetGpuInstanceId()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting GPU Instance ID: %v", ret)
	}

	ci, ret := d.GetComputeInstanceId()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting Compute Instance ID: %v", ret)
	}

	parent, ret := d.GetDeviceHandleFromMigDeviceHandle()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting parent device: %v", ret)
	}
	minor, ret := parent.GetMinorNumber()
	if ret != nvml.SUCCESS {
		return nil, fmt.Errorf("error getting GPU device minor number: %v", ret)
	}
	parentPath := fmt.Sprintf("/dev/nvidia%d", minor)

	giCapPath := fmt.Sprintf(nvidiaCapabilitiesPath+"/gpu%d/mig/gi%d/access", minor, gi)
	if _, exists := capDevicePaths[giCapPath]; !exists {
		return nil, fmt.Errorf("missing MIG GPU instance capability path: %v", giCapPath)
	}

	ciCapPath := fmt.Sprintf(nvidiaCapabilitiesPath+"/gpu%d/mig/gi%d/ci%d/access", minor, gi, ci)
	if _, exists := capDevicePaths[ciCapPath]; !exists {
		return nil, fmt.Errorf("missing MIG GPU instance capability path: %v", giCapPath)
	}

	devicePaths := []string{
		parentPath,
		capDevicePaths[giCapPath],
		capDevicePaths[ciCapPath],
	}

	return devicePaths, nil
}

func getNumaNode(d nvml.Device) (bool, int, error) {
	pciInfo, ret := d.GetPciInfo()
	if ret != nvml.SUCCESS {
		return false, 0, fmt.Errorf("error getting PCI Bus Info of device: %v", ret)
	}

	// Discard leading zeros.
	busID := strings.ToLower(strings.TrimPrefix(int8Slice(pciInfo.BusId[:]).String(), "0000"))

	b, err := os.ReadFile(fmt.Sprintf("/sys/bus/pci/devices/%s/numa_node", busID))
	if err != nil {
		return false, 0, nil
	}

	node, err := strconv.Atoi(string(bytes.TrimSpace(b)))
	if err != nil {
		return false, 0, fmt.Errorf("eror parsing value for NUMA node: %v", err)
	}

	if node < 0 {
		return false, 0, nil
	}

	return true, node, nil
}

func checkHealth(stop <-chan interface{}, devices []*Device, unhealthy chan<- *Device) {
	disableHealthChecks := strings.ToLower(os.Getenv(envDisableHealthChecks))
	if disableHealthChecks == "all" {
		disableHealthChecks = allHealthChecks
	}
	if strings.Contains(disableHealthChecks, "xids") {
		return
	}

	// FIXME: formalize the full list and document it.
	// http://docs.nvidia.com/deploy/xid-errors/index.html#topic_4
	// Application errors: the GPU should still be healthy
	applicationErrorXids := []uint64{
		13, // Graphics Engine Exception
		31, // GPU memory page fault
		43, // GPU stopped processing
		45, // Preemptive cleanup, due to previous errors
		68, // Video processor exception
	}

	skippedXids := make(map[uint64]bool)
	for _, id := range applicationErrorXids {
		skippedXids[id] = true
	}

	for _, additionalXid := range getAdditionalXids(disableHealthChecks) {
		skippedXids[additionalXid] = true
	}

	eventSet, ret := config.Nvml().EventSetCreate()
	if ret != nvml.SUCCESS {
		klog.Warningf("could not create event set: %v", ret)
		return
	}
	defer eventSet.Free()

	parentToDeviceMap := make(map[string]*Device)
	deviceIDToGiMap := make(map[string]int)
	deviceIDToCiMap := make(map[string]int)

	eventMask := uint64(nvml.EventTypeXidCriticalError | nvml.EventTypeDoubleBitEccError | nvml.EventTypeSingleBitEccError)
	for _, d := range devices {
		uuid, gi, ci, err := getDevicePlacement(d)
		if err != nil {
			klog.Warningf("Could not determine device placement for %v: %v; Marking it unhealthy.", d.ID, err)
			unhealthy <- d
			continue
		}
		deviceIDToGiMap[d.ID] = gi
		deviceIDToCiMap[d.ID] = ci
		parentToDeviceMap[uuid] = d

		gpu, ret := config.Nvml().DeviceGetHandleByUUID(uuid)
		if ret != nvml.SUCCESS {
			klog.Infof("unable to get device handle from UUID: %v; marking it as unhealthy", ret)
			unhealthy <- d
			continue
		}

		supportedEvents, ret := gpu.GetSupportedEventTypes()
		if ret != nvml.SUCCESS {
			klog.Infof("Unable to determine the supported events for %v: %v; marking it as unhealthy", d.ID, ret)
			unhealthy <- d
			continue
		}

		ret = gpu.RegisterEvents(eventMask&supportedEvents, eventSet)
		if ret == nvml.ERROR_NOT_SUPPORTED {
			klog.Warningf("Device %v is too old to support healthchecking.", d.ID)
		}
		if ret != nvml.SUCCESS {
			klog.Infof("Marking device %v as unhealthy: %v", d.ID, ret)
			unhealthy <- d
		}
	}

	for {
		select {
		case <-stop:
			return
		default:
		}

		e, ret := eventSet.Wait(5000)
		if ret == nvml.ERROR_TIMEOUT {
			continue
		}
		if ret != nvml.SUCCESS {
			klog.Infof("Error waiting for event: %v; Marking all devices as unhealthy", ret)
			for _, d := range devices {
				unhealthy <- d
			}
			continue
		}

		if e.EventType != nvml.EventTypeXidCriticalError {
			klog.Infof("Skipping non-nvmlEventTypeXidCriticalError event: %+v", e)
			continue
		}

		if skippedXids[e.EventData] {
			klog.Infof("Skipping event %+v", e)
			continue
		}

		klog.Infof("Processing event %+v", e)
		eventUUID, ret := e.Device.GetUUID()
		if ret != nvml.SUCCESS {
			// If we cannot reliably determine the device UUID, we mark all devices as unhealthy.
			klog.Infof("Failed to determine uuid for event %v: %v; Marking all devices as unhealthy.", e, ret)
			for _, d := range devices {
				unhealthy <- d
			}
			continue
		}

		d, exists := parentToDeviceMap[eventUUID]
		if !exists {
			klog.Infof("Ignoring event for unexpected device: %v", eventUUID)
			continue
		}

		if d.IsMigDevice() && e.GpuInstanceId != 0xFFFFFFFF && e.ComputeInstanceId != 0xFFFFFFFF {
			gi := deviceIDToGiMap[d.ID]
			ci := deviceIDToCiMap[d.ID]
			if !(uint32(gi) == e.GpuInstanceId && uint32(ci) == e.ComputeInstanceId) {
				continue
			}
			klog.Infof("Event for mig device %v (gi=%v, ci=%v)", d.ID, gi, ci)
		}

		klog.Infof("XidCriticalError: Xid=%d on Device=%s; marking device as unhealthy.", e.EventData, d.ID)
		unhealthy <- d
	}
}

// getAdditionalXids returns a list of additional Xids to skip from the specified string.
// The input is treaded as a comma-separated string and all valid uint64 values are considered as Xid values. Invalid values
// are ignored.
func getAdditionalXids(input string) []uint64 {
	if input == "" {
		return nil
	}

	var additionalXids []uint64
	for _, additionalXid := range strings.Split(input, ",") {
		trimmed := strings.TrimSpace(additionalXid)
		if trimmed == "" {
			continue
		}
		xid, err := strconv.ParseUint(trimmed, 10, 64)
		if err != nil {
			log.Printf("Ignoring malformed Xid value %v: %v", trimmed, err)
			continue
		}
		additionalXids = append(additionalXids, xid)
	}

	return additionalXids
}

// getDevicePlacement returns the placement of the specified device.
// For a MIG device the placement is defined by the 3-tuple <parent UUID, GI, CI>
// For a full device the returned 3-tuple is the device's uuid and 0xFFFFFFFF for the other two elements.
func getDevicePlacement(d *Device) (string, int, int, error) {
	if !d.IsMigDevice() {
		return d.GetUUID(), 0xFFFFFFFF, 0xFFFFFFFF, nil
	}
	return getMigDeviceParts(d)
}

// getMigDeviceParts returns the parent GI and CI ids of the MIG device.
func getMigDeviceParts(d *Device) (string, int, int, error) {
	if !d.IsMigDevice() {
		return "", 0, 0, fmt.Errorf("cannot get GI and CI of full device")
	}

	uuid := d.GetUUID()
	// For older driver versions, the call to DeviceGetHandleByUUID will fail for MIG devices.
	mig, ret := config.Nvml().DeviceGetHandleByUUID(uuid)
	if ret == nvml.SUCCESS {
		parentHandle, ret := mig.GetDeviceHandleFromMigDeviceHandle()
		if ret != nvml.SUCCESS {
			return "", 0, 0, fmt.Errorf("failed to get parent device handle: %v", ret)
		}

		parentUUID, ret := parentHandle.GetUUID()
		if ret != nvml.SUCCESS {
			return "", 0, 0, fmt.Errorf("failed to get parent uuid: %v", ret)
		}
		gi, ret := mig.GetGpuInstanceId()
		if ret != nvml.SUCCESS {
			return "", 0, 0, fmt.Errorf("failed to get GPU Instance ID: %v", ret)
		}

		ci, ret := mig.GetComputeInstanceId()
		if ret != nvml.SUCCESS {
			return "", 0, 0, fmt.Errorf("failed to get Compute Instance ID: %v", ret)
		}
		return parentUUID, gi, ci, nil
	}
	return parseMigDeviceUUID(uuid)
}

// parseMigDeviceUUID splits the MIG device UUID into the parent device UUID and ci and gi
func parseMigDeviceUUID(mig string) (string, int, int, error) {
	tokens := strings.SplitN(mig, "-", 2)
	if len(tokens) != 2 || tokens[0] != "MIG" {
		return "", 0, 0, fmt.Errorf("unable to parse UUID as MIG device")
	}

	tokens = strings.SplitN(tokens[1], "/", 3)
	if len(tokens) != 3 || !strings.HasPrefix(tokens[0], "GPU-") {
		return "", 0, 0, fmt.Errorf("unable to parse UUID as MIG device")
	}

	gi, err := strconv.ParseInt(tokens[1], 10, 32)
	if err != nil {
		return "", 0, 0, fmt.Errorf("unable to parse UUID as MIG device")
	}

	ci, err := strconv.ParseInt(tokens[2], 10, 32)
	if err != nil {
		return "", 0, 0, fmt.Errorf("unable to parse UUID as MIG device")
	}

	return tokens[0], int(gi), int(ci), nil
}

// IsMigDevice returns checks whether d is a MIG device or not.
func (d Device) IsMigDevice() bool {
	return strings.Contains(d.Index, ":")
}

// GetUUID returns the UUID for the device from the annotated ID.
func (d Device) GetUUID() string {
	return AnnotatedID(d.ID).GetID()
}

// AnnotatedID represents an ID with a replica number embedded in it.
type AnnotatedID string

// Split splits a AnnotatedID into its ID and replica number parts.
func (r AnnotatedID) Split() (string, int) {
	split := strings.SplitN(string(r), "::", 2)
	if len(split) != 2 {
		return string(r), 0
	}
	replica, _ := strconv.ParseInt(split[1], 10, 0)
	return split[0], int(replica)
}

// GetID returns just the ID part of the replicated ID
func (r AnnotatedID) GetID() string {
	id, _ := r.Split()
	return id
}
