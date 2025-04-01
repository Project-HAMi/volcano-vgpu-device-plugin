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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/klog/v2"
	"volcano.sh/k8s-device-plugin/pkg/lock"
	"volcano.sh/k8s-device-plugin/pkg/plugin/vgpu/config"
	"volcano.sh/k8s-device-plugin/pkg/plugin/vgpu/util"

	"github.com/NVIDIA/go-gpuallocator/gpuallocator"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

// Constants to represent the various device list strategies
const (
	DeviceListStrategyEnvvar       = "envvar"
	DeviceListStrategyVolumeMounts = "volume-mounts"
)

// Constants to represent the various device id strategies
const (
	DeviceIDStrategyUUID  = "uuid"
	DeviceIDStrategyIndex = "index"
)

// Constants for use by the 'volume-mounts' device list strategy
const (
	deviceListAsVolumeMountsHostPath          = "/dev/null"
	deviceListAsVolumeMountsContainerPathRoot = "/var/run/nvidia-container-devices"
)

// NvidiaDevicePlugin implements the Kubernetes device plugin API
type NvidiaDevicePlugin struct {
	ResourceManager
	deviceCache      *DeviceCache
	resourceName     string
	deviceListEnvvar string
	allocatePolicy   gpuallocator.Policy
	socket           string
	schedulerConfig  config.NvidiaConfig
	operatingMode    string

	virtualDevices []*pluginapi.Device
	migCurrent     config.MigPartedSpec

	server        *grpc.Server
	cachedDevices []*Device
	health        chan *Device
	stop          chan interface{}
	changed       chan struct{}
	migStrategy   string
}

var (
	// DevicePluginFilterDevice need device-plugin filter this device, don't register this device.
	DevicePluginFilterDevice *config.FilterDevice
)

func readFromConfigFile(sConfig *config.NvidiaConfig) (string, error) {
	config.Mode = "hami-core"
	jsonbyte, err := os.ReadFile("/config/config.json")
	if err != nil {
		return "", err
	}
	var deviceConfigs config.DevicePluginConfigs
	err = json.Unmarshal(jsonbyte, &deviceConfigs)
	if err != nil {
		return "", err
	}
	klog.Infof("Device Plugin Configs: %v", fmt.Sprintf("%v", deviceConfigs))
	for _, val := range deviceConfigs.Nodeconfig {
		if os.Getenv("NODE_NAME") == val.Name {
			klog.Infof("Reading config from file %s", val.Name)
			if val.Devicememoryscaling > 0 {
				sConfig.DeviceMemoryScaling = val.Devicememoryscaling
			}
			if val.Devicecorescaling > 0 {
				sConfig.DeviceCoreScaling = val.Devicecorescaling
			}
			if val.Devicesplitcount > 0 {
				sConfig.DeviceSplitCount = val.Devicesplitcount
			}
			if val.FilterDevice != nil && (len(val.FilterDevice.UUID) > 0 || len(val.FilterDevice.Index) > 0) {
				DevicePluginFilterDevice = val.FilterDevice
			}
			if len(val.OperatingMode) > 0 {
				config.Mode = val.OperatingMode
			}
			klog.Infof("FilterDevice: %v", val.FilterDevice)
		}
	}
	return config.Mode, nil
}

// NewNvidiaDevicePlugin returns an initialized NvidiaDevicePlugin
func NewNvidiaDevicePlugin(resourceName string, deviceCache *DeviceCache, allocatePolicy gpuallocator.Policy, socket string) *NvidiaDevicePlugin {
	configs, err := util.LoadConfigFromCM("volcano-vgpu-device-config")
	if err != nil {
		klog.InfoS("configMap not found", err.Error())
	}
	nvidiaConfig := config.NvidiaConfig{}
	if configs != nil {
		nvidiaConfig = configs.NvidiaConfig
	}
	nvidiaConfig.DeviceSplitCount = config.DeviceSplitCount
	nvidiaConfig.DeviceCoreScaling = config.DeviceCoresScaling
	nvidiaConfig.GPUMemoryFactor = config.GPUMemoryFactor
	mode, err := readFromConfigFile(&nvidiaConfig)
	if err != nil {
		klog.InfoS("readFrom device cm error", err.Error())
		return nil
	}
	klog.Infoln("Loaded config=", nvidiaConfig)
	dp := &NvidiaDevicePlugin{
		deviceCache:     deviceCache,
		resourceName:    resourceName,
		allocatePolicy:  allocatePolicy,
		socket:          socket,
		migStrategy:     "none",
		operatingMode:   mode,
		schedulerConfig: nvidiaConfig,
		// These will be reinitialized every
		// time the plugin server is restarted.
		server: nil,
		health: nil,
		stop:   nil,
	}
	return dp
}

// NewNvidiaDevicePlugin returns an initialized NvidiaDevicePlugin
func NewMIGNvidiaDevicePlugin(resourceName string, resourceManager ResourceManager, deviceListEnvvar string, allocatePolicy gpuallocator.Policy, socket string) *NvidiaDevicePlugin {
	return &NvidiaDevicePlugin{
		ResourceManager:  resourceManager,
		resourceName:     resourceName,
		deviceListEnvvar: deviceListEnvvar,
		allocatePolicy:   allocatePolicy,
		socket:           socket,

		// These will be reinitialized every
		// time the plugin server is restarted.
		cachedDevices: nil,
		server:        nil,
		health:        nil,
		stop:          nil,
		migStrategy:   "mixed",
	}
}

func (m *NvidiaDevicePlugin) initialize() {
	var err error
	if strings.Compare(m.migStrategy, "mixed") == 0 {
		m.cachedDevices = m.ResourceManager.Devices()
	}
	m.server = grpc.NewServer([]grpc.ServerOption{}...)
	m.health = make(chan *Device)
	m.stop = make(chan interface{})
	check(err)

	m.virtualDevices, _ = util.GetDevices(config.GPUMemoryFactor)
}

func (m *NvidiaDevicePlugin) cleanup() {
	close(m.stop)
	m.server = nil
	m.health = nil
	m.stop = nil
}

// Start starts the gRPC server, registers the device plugin with the Kubelet,
// and starts the device healthchecks.
func (m *NvidiaDevicePlugin) Start() error {
	m.initialize()

	err := m.Serve()
	if err != nil {
		log.Printf("Could not start device plugin for '%s': %s", m.resourceName, err)
		m.cleanup()
		return err
	}
	log.Printf("Starting to serve '%s' on %s", m.resourceName, m.socket)

	err = m.Register()
	if err != nil {
		log.Printf("Could not register device plugin: %s", err)
		m.Stop()
		return err
	}
	log.Printf("Registered device plugin for '%s' with Kubelet", m.resourceName)

	if m.operatingMode == "mig" {
		cmd := exec.Command("nvidia-mig-parted", "export")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil {
			klog.Fatalf("nvidia-mig-parted failed with %s\n", err)
		}
		outStr := stdout.Bytes()
		yaml.Unmarshal(outStr, &m.migCurrent)
		os.WriteFile("/tmp/migconfig.yaml", outStr, os.ModePerm)
		if len(m.migCurrent.MigConfigs["current"]) == 1 && len(m.migCurrent.MigConfigs["current"][0].Devices) == 0 {
			idx := 0
			m.migCurrent.MigConfigs["current"][0].Devices = make([]int32, 0)
			for idx < util.GetDeviceNums() {
				m.migCurrent.MigConfigs["current"][0].Devices = append(m.migCurrent.MigConfigs["current"][0].Devices, int32(idx))
				idx++
			}
		}
		klog.Infoln("Mig export", m.migCurrent)
	}

	if strings.Compare(m.migStrategy, "none") == 0 {
		m.deviceCache.AddNotifyChannel("plugin", m.health)
	} else if strings.Compare(m.migStrategy, "mixed") == 0 {
		go m.CheckHealth(m.stop, m.cachedDevices, m.health)
	} else {
		log.Panicln("migstrategy not recognized", m.migStrategy)
	}
	return nil
}

// Stop stops the gRPC server.
func (m *NvidiaDevicePlugin) Stop() error {
	if m == nil || m.server == nil {
		return nil
	}
	log.Printf("Stopping to serve '%s' on %s", m.resourceName, m.socket)
	m.deviceCache.RemoveNotifyChannel("plugin")
	m.server.Stop()
	if err := os.Remove(m.socket); err != nil && !os.IsNotExist(err) {
		return err
	}
	m.cleanup()
	return nil
}

// Serve starts the gRPC server of the device plugin.
func (m *NvidiaDevicePlugin) Serve() error {
	os.Remove(m.socket)
	sock, err := net.Listen("unix", m.socket)
	if err != nil {
		return err
	}

	pluginapi.RegisterDevicePluginServer(m.server, m)

	go func() {
		lastCrashTime := time.Now()
		restartCount := 0
		for {
			log.Printf("Starting GRPC server for '%s'", m.resourceName)
			err := m.server.Serve(sock)
			if err == nil {
				break
			}

			log.Printf("GRPC server for '%s' crashed with error: %v", m.resourceName, err)

			// restart if it has not been too often
			// i.e. if server has crashed more than 5 times and it didn't last more than one hour each time
			if restartCount > 5 {
				// quit
				log.Fatalf("GRPC server for '%s' has repeatedly crashed recently. Quitting", m.resourceName)
			}
			timeSinceLastCrash := time.Since(lastCrashTime).Seconds()
			lastCrashTime = time.Now()
			if timeSinceLastCrash > 3600 {
				// it has been one hour since the last crash.. reset the count
				// to reflect on the frequency
				restartCount = 1
			} else {
				restartCount++
			}
		}
	}()

	// Wait for server to start by launching a blocking connexion
	conn, err := m.dial(m.socket, 5*time.Second)
	if err != nil {
		return err
	}
	conn.Close()

	return nil
}

// Register registers the device plugin for the given resourceName with Kubelet.
func (m *NvidiaDevicePlugin) Register() error {
	conn, err := m.dial(pluginapi.KubeletSocket, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)
	reqt := &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     path.Base(m.socket),
		ResourceName: m.resourceName,
		Options:      &pluginapi.DevicePluginOptions{},
	}

	_, err = client.Register(context.Background(), reqt)
	if err != nil {
		return err
	}
	return nil
}

// GetDevicePluginOptions returns the values of the optional settings for this plugin
func (m *NvidiaDevicePlugin) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	options := &pluginapi.DevicePluginOptions{}
	return options, nil
}

// ListAndWatch lists devices and update that list according to the health status
func (m *NvidiaDevicePlugin) ListAndWatch(e *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
	if m.resourceName == util.ResourceMem {
		err := s.Send(&pluginapi.ListAndWatchResponse{Devices: m.virtualDevices})
		if err != nil {
			log.Fatalf("failed sending devices %d: %v", len(m.virtualDevices), err)
		}

		for {
			select {
			case <-m.stop:
				return nil
			case d := <-m.health:
				// FIXME: there is no way to recover from the Unhealthy state.
				//isChange := false
				//if d.Health != pluginapi.Unhealthy {
				//isChange = true
				//}
				d.Health = pluginapi.Unhealthy
				log.Printf("'%s' device marked unhealthy: %s", m.resourceName, d.ID)
				s.Send(&pluginapi.ListAndWatchResponse{Devices: m.virtualDevices})
				//if isChange {
				//	m.kubeInteractor.PatchUnhealthyGPUListOnNode(m.physicalDevices)
				//}
			}
		}

	} else {
		_ = s.Send(&pluginapi.ListAndWatchResponse{Devices: m.apiDevices()})
		for {
			select {
			case <-m.stop:
				return nil
			case d := <-m.health:
				// FIXME: there is no way to recover from the Unhealthy state.
				//d.Health = pluginapi.Unhealthy
				log.Printf("'%s' device marked unhealthy: %s", m.resourceName, d.ID)
				_ = s.Send(&pluginapi.ListAndWatchResponse{Devices: m.apiDevices()})
			}
		}
	}
}

func (m *NvidiaDevicePlugin) MIGAllocate(ctx context.Context, reqs *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	responses := pluginapi.AllocateResponse{}
	for _, req := range reqs.ContainerRequests {
		for _, id := range req.DevicesIDs {
			if !m.deviceExists(id) {
				return nil, fmt.Errorf("invalid allocation request for '%s': unknown device: %s", m.resourceName, id)
			}
		}

		response := pluginapi.ContainerAllocateResponse{}

		uuids := req.DevicesIDs
		deviceIDs := m.deviceIDsFromUUIDs(uuids)

		response.Envs = m.apiEnvs(m.deviceListEnvvar, deviceIDs)

		klog.Infof("response=", response.Envs)
		responses.ContainerResponses = append(responses.ContainerResponses, &response)
	}

	return &responses, nil
}

// Allocate which return list of devices.
func (m *NvidiaDevicePlugin) Allocate(ctx context.Context, reqs *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	if len(reqs.ContainerRequests) > 1 {
		return &pluginapi.AllocateResponse{}, errors.New("multiple Container Requests not supported")
	}
	if strings.Compare(m.migStrategy, "mixed") == 0 {
		return m.MIGAllocate(ctx, reqs)
	}
	responses := pluginapi.AllocateResponse{}

	if strings.Compare(m.resourceName, util.ResourceMem) == 0 || strings.Compare(m.resourceName, util.ResourceCores) == 0 {
		for range reqs.ContainerRequests {
			responses.ContainerResponses = append(responses.ContainerResponses, &pluginapi.ContainerAllocateResponse{})
		}
		return &responses, nil
	}
	nodename := os.Getenv("NODE_NAME")

	current, err := util.GetPendingPod(nodename)
	if err != nil {
		lock.ReleaseNodeLock(nodename, util.VGPUDeviceName)
		return &pluginapi.AllocateResponse{}, err
	}
	if current == nil {
		klog.Errorf("no pending pod found on node %s", nodename)
		lock.ReleaseNodeLock(nodename, util.VGPUDeviceName)
		return &pluginapi.AllocateResponse{}, errors.New("no pending pod found on node")
	}

	for idx := range reqs.ContainerRequests {
		currentCtr, devreq, err := util.GetNextDeviceRequest(util.NvidiaGPUDevice, *current)
		klog.Infoln("deviceAllocateFromAnnotation=", devreq)
		if err != nil {
			klog.Errorln("get device from annotation failed", err.Error())
			util.PodAllocationFailed(nodename, current)
			return &pluginapi.AllocateResponse{}, err
		}
		if len(devreq) != len(reqs.ContainerRequests[idx].DevicesIDs) {
			klog.Errorln("device number not matched", devreq, reqs.ContainerRequests[idx].DevicesIDs)
			util.PodAllocationFailed(nodename, current)
			return &pluginapi.AllocateResponse{}, errors.New("device number not matched")
		}

		response := pluginapi.ContainerAllocateResponse{}
		response.Envs = make(map[string]string)
		response.Envs["NVIDIA_VISIBLE_DEVICES"] = strings.Join(m.GetContainerDeviceStrArray(devreq), ",")

		err = util.EraseNextDeviceTypeFromAnnotation(util.NvidiaGPUDevice, *current)
		if err != nil {
			klog.Errorln("Erase annotation failed", err.Error())
			util.PodAllocationFailed(nodename, current)
			return &pluginapi.AllocateResponse{}, err
		}

		if m.operatingMode != "mig" {

			for i, dev := range devreq {
				limitKey := fmt.Sprintf("CUDA_DEVICE_MEMORY_LIMIT_%v", i)
				response.Envs[limitKey] = fmt.Sprintf("%vm", dev.Usedmem*int32(config.GPUMemoryFactor))
			}
			response.Envs["CUDA_DEVICE_SM_LIMIT"] = fmt.Sprint(devreq[0].Usedcores)
			response.Envs["CUDA_DEVICE_MEMORY_SHARED_CACHE"] = fmt.Sprintf("/tmp/vgpu/%v.cache", uuid.NewUUID())

			cacheFileHostDirectory := "/tmp/vgpu/containers/" + string(current.UID) + "_" + currentCtr.Name
			os.MkdirAll(cacheFileHostDirectory, 0777)
			os.Chmod(cacheFileHostDirectory, 0777)
			os.MkdirAll("/tmp/vgpulock", 0777)
			os.Chmod("/tmp/vgpulock", 0777)
			hostHookPath := os.Getenv("HOOK_PATH")

			response.Mounts = append(response.Mounts,
				&pluginapi.Mount{ContainerPath: "/usr/local/vgpu/libvgpu.so",
					HostPath: hostHookPath + "/libvgpu.so",
					ReadOnly: true},
				&pluginapi.Mount{ContainerPath: "/tmp/vgpu",
					HostPath: cacheFileHostDirectory,
					ReadOnly: false},
				&pluginapi.Mount{ContainerPath: "/tmp/vgpulock",
					HostPath: "/tmp/vgpulock",
					ReadOnly: false},
			)
			found := false
			for _, val := range currentCtr.Env {
				if strings.Compare(val.Name, "CUDA_DISABLE_CONTROL") == 0 {
					found = true
					break
				}
			}
			if !found {
				response.Mounts = append(response.Mounts, &pluginapi.Mount{ContainerPath: "/etc/ld.so.preload",
					HostPath: hostHookPath + "/ld.so.preload",
					ReadOnly: true},
				)
			}
		}
		responses.ContainerResponses = append(responses.ContainerResponses, &response)
	}
	klog.Infoln("Allocate Response", responses.ContainerResponses)
	util.PodAllocationTrySuccess(nodename, current)
	return &responses, nil
}

// PreStartContainer is unimplemented for this plugin
func (m *NvidiaDevicePlugin) PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

// dial establishes the gRPC communication with the registered device plugin.
func (m *NvidiaDevicePlugin) dial(unixSocketPath string, timeout time.Duration) (*grpc.ClientConn, error) {
	c, err := grpc.Dial(unixSocketPath, grpc.WithInsecure(), grpc.WithBlock(),
		grpc.WithTimeout(timeout),
		grpc.WithDialer(func(addr string, timeout time.Duration) (net.Conn, error) {
			return net.DialTimeout("unix", addr, timeout)
		}),
	)

	if err != nil {
		return nil, err
	}

	return c, nil
}

func (m *NvidiaDevicePlugin) Devices() []*Device {
	if strings.Compare(m.migStrategy, "none") == 0 {
		return m.deviceCache.GetCache()
	}
	if strings.Compare(m.migStrategy, "mixed") == 0 {
		return m.ResourceManager.Devices()
	}
	log.Panic("migStrategy not recognized,exiting...")
	return []*Device{}
}

func (m *NvidiaDevicePlugin) deviceExists(id string) bool {
	for _, d := range m.cachedDevices {
		if d.ID == id {
			return true
		}
	}
	return false
}

func (m *NvidiaDevicePlugin) deviceIDsFromUUIDs(uuids []string) []string {
	return uuids
}

func (m *NvidiaDevicePlugin) apiDevices() []*pluginapi.Device {
	if strings.Compare(m.migStrategy, "mixed") == 0 {
		var pdevs []*pluginapi.Device
		for _, d := range m.cachedDevices {
			pdevs = append(pdevs, &d.Device)
		}
		return pdevs
	}
	devices := m.Devices()
	var res []*pluginapi.Device

	if strings.Compare(m.resourceName, util.ResourceMem) == 0 {
		for _, dev := range devices {
			i := 0
			klog.Infoln("memory=", dev.Memory, "id=", dev.ID)
			for i < int(32767) {
				res = append(res, &pluginapi.Device{
					ID:       fmt.Sprintf("%v-memory-%v", dev.ID, i),
					Health:   dev.Health,
					Topology: nil,
				})
				i++
			}
		}
		klog.Infoln("res length=", len(res))
		return res
	}
	if strings.Compare(m.resourceName, util.ResourceCores) == 0 {
		for _, dev := range devices {
			i := 0
			for i < 100 {
				res = append(res, &pluginapi.Device{
					ID:       fmt.Sprintf("%v-core-%v", dev.ID, i),
					Health:   dev.Health,
					Topology: nil,
				})
				i++
			}
		}
		return res
	}

	for _, dev := range devices {
		for i := uint(0); i < config.DeviceSplitCount; i++ {
			id := fmt.Sprintf("%v-%v", dev.ID, i)
			res = append(res, &pluginapi.Device{
				ID:       id,
				Health:   dev.Health,
				Topology: nil,
			})
		}
	}
	return res
}

func (m *NvidiaDevicePlugin) apiEnvs(envvar string, deviceIDs []string) map[string]string {
	return map[string]string{
		envvar: strings.Join(deviceIDs, ","),
	}
}

func (m *NvidiaDevicePlugin) ApplyMigTemplate() {
	data, err := yaml.Marshal(m.migCurrent)
	if err != nil {
		klog.Error("marshal failed", err.Error())
	}
	klog.Infoln("Applying data=", string(data))
	os.WriteFile("/tmp/migconfig.yaml", data, os.ModePerm)
	cmd := exec.Command("nvidia-mig-parted", "apply", "-f", "/tmp/migconfig.yaml")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		klog.Fatalf("nvidia-mig-parted failed with %s\n", err)
	}
	outStr := stdout.String()
	klog.Infoln("Mig apply", outStr)
}

func (m *NvidiaDevicePlugin) GetContainerDeviceStrArray(c util.ContainerDevices) []string {
	tmp := []string{}
	needsreset := false
	position := 0
	for _, val := range c {
		if !strings.Contains(val.UUID, "[") {
			tmp = append(tmp, val.UUID)
		} else {
			devtype, devindex := util.GetIndexAndTypeFromUUID(val.UUID)
			position, needsreset = m.GenerateMigTemplate(devtype, devindex, val)
			if needsreset {
				m.ApplyMigTemplate()
			}
			tmp = append(tmp, util.GetMigUUIDFromIndex(val.UUID, position))
		}
	}
	klog.V(3).Infoln("mig current=", m.migCurrent, ":", needsreset, "position=", position, "uuid lists", tmp)
	return tmp
}

func (m *NvidiaDevicePlugin) GenerateMigTemplate(devtype string, devindex int, val util.ContainerDevice) (int, bool) {
	needsreset := false
	position := -1 // Initialize to an invalid position

	for _, migTemplate := range m.schedulerConfig.MigGeometriesList {
		if containsModel(devtype, migTemplate.Models) {
			klog.InfoS("type found", "Type", devtype, "Models", strings.Join(migTemplate.Models, ", "))

			templateGroupName, pos, err := util.ExtractMigTemplatesFromUUID(val.UUID)
			if err != nil {
				klog.ErrorS(err, "failed to extract template index from UUID", "UUID", val.UUID)
				return -1, false
			}

			templateIdx := -1
			for i, migTemplateEntry := range migTemplate.Geometries {
				if migTemplateEntry.Group == templateGroupName {
					templateIdx = i
					break
				}
			}

			if templateIdx < 0 || templateIdx >= len(migTemplate.Geometries) {
				klog.ErrorS(nil, "invalid template index extracted from UUID", "UUID", val.UUID, "Index", templateIdx)
				return -1, false
			}

			position = pos

			v := migTemplate.Geometries[templateIdx].Instances

			for migidx, migpartedDev := range m.migCurrent.MigConfigs["current"] {
				if containsDevice(devindex, migpartedDev.Devices) {
					for _, migTemplateEntry := range v {
						currentCount, ok := migpartedDev.MigDevices[migTemplateEntry.Name]
						expectedCount := migTemplateEntry.Count

						if !ok || currentCount != expectedCount {
							needsreset = true
							klog.InfoS("updated mig device count", "Template", v)
						} else {
							klog.InfoS("incremented mig device count", "TemplateName", migTemplateEntry.Name, "Count", currentCount+1)
						}
					}

					if needsreset {
						for k := range m.migCurrent.MigConfigs["current"][migidx].MigDevices {
							delete(m.migCurrent.MigConfigs["current"][migidx].MigDevices, k)
						}

						for _, migTemplateEntry := range v {
							m.migCurrent.MigConfigs["current"][migidx].MigDevices[migTemplateEntry.Name] = migTemplateEntry.Count
							m.migCurrent.MigConfigs["current"][migidx].MigEnabled = true
						}
					}
					break
				}
			}
			break
		}
	}

	return position, needsreset
}

// Helper function to check if a model is in the list of models.
func containsModel(target string, models []string) bool {
	for _, model := range models {
		if strings.Contains(target, model) {
			return true
		}
	}
	return false
}

// Helper function to check if a device index is in the list of devices.
func containsDevice(target int, devices []int32) bool {
	for _, device := range devices {
		if int(device) == target {
			return true
		}
	}
	return false
}
