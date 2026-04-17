/*
 * Copyright (c) 2019, NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"

	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/cdi"
	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/imex"
	"volcano.sh/k8s-device-plugin/pkg/rm"
	"volcano.sh/k8s-device-plugin/pkg/util"
	"volcano.sh/k8s-device-plugin/pkg/util/nodelock"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gopkg.in/yaml.v2"

	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	deviceListEnvVar                          = "NVIDIA_VISIBLE_DEVICES"
	deviceListAsVolumeMountsHostPath          = "/dev/null"
	deviceListAsVolumeMountsContainerPathRoot = "/var/run/nvidia-container-devices"
)

// nvidiaDevicePlugin implements the Kubernetes device plugin API
type nvidiaDevicePlugin struct {
	pluginapi.UnimplementedDevicePluginServer
	ctx                  context.Context
	rm                   rm.ResourceManager
	config               *spec.Config
	deviceListStrategies spec.DeviceListStrategies

	cdiHandler          cdi.Interface
	cdiAnnotationPrefix string

	socket string
	server *grpc.Server
	health chan *rm.Device
	stop   chan interface{}

	imexChannels imex.Channels

	mps mpsOptions

	migCurrent config.MigPartedSpec
}

// devicePluginForResource creates a device plugin for the specified resource.
func (o *options) devicePluginForResource(ctx context.Context, resourceManager rm.ResourceManager) (Interface, error) {
	mpsOptions, err := o.getMPSOptions(resourceManager)
	if err != nil {
		return nil, err
	}

	plugin := nvidiaDevicePlugin{
		ctx:                  ctx,
		rm:                   resourceManager,
		config:               o.config,
		deviceListStrategies: o.deviceListStrategies,

		cdiHandler:          o.cdiHandler,
		cdiAnnotationPrefix: *o.config.Flags.Plugin.CDIAnnotationPrefix,

		imexChannels: o.imexChannels,

		mps: mpsOptions,

		socket: getPluginSocketPath(resourceManager.Resource()),
		// These will be reinitialized every
		// time the plugin server is restarted.
		server: nil,
		health: nil,
		stop:   nil,
	}
	return &plugin, nil
}

// getPluginSocketPath returns the socket to use for the specified resource.
func getPluginSocketPath(resource spec.ResourceName) string {
	_, name := resource.Split()
	pluginName := "nvidia-" + name
	return filepath.Join(pluginapi.DevicePluginPath, pluginName) + ".sock"
}

func (plugin *nvidiaDevicePlugin) initialize() {
	plugin.server = grpc.NewServer([]grpc.ServerOption{}...)
	plugin.health = make(chan *rm.Device)
	plugin.stop = make(chan interface{})
}

func (plugin *nvidiaDevicePlugin) cleanup() {
	close(plugin.stop)
	plugin.server = nil
	plugin.health = nil
	plugin.stop = nil
}

// Devices returns the full set of devices associated with the plugin.
func (plugin *nvidiaDevicePlugin) Devices() rm.Devices {
	return plugin.rm.Devices()
}

// Start starts the gRPC server, registers the device plugin with the Kubelet,
// and starts the device healthchecks.
func (plugin *nvidiaDevicePlugin) Start(kubeletSocket string) error {
	plugin.initialize()

	deviceNumbers, err := util.GetDeviceNums()
	if err != nil {
		return err
	}

	if err := plugin.mps.waitForDaemon(); err != nil {
		return fmt.Errorf("error waiting for MPS daemon: %w", err)
	}

	if err := plugin.Serve(); err != nil {
		klog.Errorf("Could not start device plugin for '%s': %s", plugin.rm.Resource(), err)
		plugin.cleanup()
		return err
	}
	klog.Infof("Starting to serve '%s' on %s", plugin.rm.Resource(), plugin.socket)

	err = plugin.Register(kubeletSocket)
	if err != nil {
		klog.Errorf("Could not register device plugin: %s", err)
		return errors.Join(err, plugin.Stop())
	}
	klog.Infof("Registered device plugin for '%s' with Kubelet", plugin.rm.Resource())

	go func() {
		// TODO: add MPS health check
		err := plugin.rm.CheckHealth(plugin.stop, plugin.health)
		if err != nil {
			klog.Errorf("Failed to start health check: %v; continuing with health checks disabled", err)
		}
	}()
	if plugin.rm.Resource() == spec.ResourceName(util.ResourceName) {
		if config.Mode == "mig" {
			cmd := exec.Command("nvidia-mig-parted", "export")
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err != nil {
				klog.Fatalf("nvidia-mig-parted failed with %s\n", err)
			}
			outStr := stdout.Bytes()
			yaml.Unmarshal(outStr, &plugin.migCurrent)
			os.WriteFile("/tmp/migconfig.yaml", outStr, os.ModePerm)
			hamiInitMigConfig, err := plugin.processMigConfigs(plugin.migCurrent.MigConfigs, deviceNumbers)
			if err != nil {
				klog.Infof("no device in node:%v", err)
			}
			plugin.migCurrent.MigConfigs["current"] = hamiInitMigConfig
			klog.Infoln("Mig export", plugin.migCurrent)
		}

		go plugin.WatchAndRegister()
	}
	return nil
}

// Stop stops the gRPC server.
func (plugin *nvidiaDevicePlugin) Stop() error {
	if plugin == nil || plugin.server == nil {
		return nil
	}
	klog.Infof("Stopping to serve '%s' on %s", plugin.rm.Resource(), plugin.socket)
	plugin.server.Stop()
	if err := os.Remove(plugin.socket); err != nil && !os.IsNotExist(err) {
		return err
	}
	plugin.cleanup()
	return nil
}

// Serve starts the gRPC server of the device plugin.
func (plugin *nvidiaDevicePlugin) Serve() error {
	os.Remove(plugin.socket)
	sock, err := net.Listen("unix", plugin.socket)
	if err != nil {
		return err
	}

	pluginapi.RegisterDevicePluginServer(plugin.server, plugin)

	go func() {
		lastCrashTime := time.Now()
		restartCount := 0

		for {
			// quite if it has been restarted too often
			// i.e. if server has crashed more than 5 times and it didn't last more than one hour each time
			if restartCount > 5 {
				// quit
				klog.Fatalf("GRPC server for '%s' has repeatedly crashed recently. Quitting", plugin.rm.Resource())
			}

			klog.Infof("Starting GRPC server for '%s'", plugin.rm.Resource())
			err := plugin.server.Serve(sock)
			if err == nil {
				break
			}

			klog.Infof("GRPC server for '%s' crashed with error: %v", plugin.rm.Resource(), err)

			timeSinceLastCrash := time.Since(lastCrashTime).Seconds()
			lastCrashTime = time.Now()
			if timeSinceLastCrash > 3600 {
				// it has been one hour since the last crash.. reset the count
				// to reflect on the frequency
				restartCount = 0
			} else {
				restartCount++
			}
		}
	}()

	// Wait for server to start by launching a blocking connection
	conn, err := plugin.dial(plugin.socket, 5*time.Second)
	if err != nil {
		return err
	}
	conn.Close()

	return nil
}

// Register registers the device plugin for the given resourceName with Kubelet.
func (plugin *nvidiaDevicePlugin) Register(kubeletSocket string) error {
	if kubeletSocket == "" {
		klog.Info("Skipping registration with Kubelet")
		return nil
	}

	conn, err := plugin.dial(kubeletSocket, 5*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pluginapi.NewRegistrationClient(conn)
	reqt := &pluginapi.RegisterRequest{
		Version:      pluginapi.Version,
		Endpoint:     path.Base(plugin.socket),
		ResourceName: string(plugin.rm.Resource()),
		Options: &pluginapi.DevicePluginOptions{
			GetPreferredAllocationAvailable: true,
		},
	}

	_, err = client.Register(plugin.ctx, reqt)
	if err != nil {
		return err
	}
	return nil
}

// GetDevicePluginOptions returns the values of the optional settings for this plugin
func (plugin *nvidiaDevicePlugin) GetDevicePluginOptions(context.Context, *pluginapi.Empty) (*pluginapi.DevicePluginOptions, error) {
	options := &pluginapi.DevicePluginOptions{
		GetPreferredAllocationAvailable: true,
	}
	return options, nil
}

// ListAndWatch lists devices and update that list according to the health status
func (plugin *nvidiaDevicePlugin) ListAndWatch(e *pluginapi.Empty, s pluginapi.DevicePlugin_ListAndWatchServer) error {
	if err := s.Send(&pluginapi.ListAndWatchResponse{Devices: plugin.apiDevices()}); err != nil {
		return err
	}
	for {
		select {
		case <-plugin.stop:
			return nil
		case d := <-plugin.health:
			// FIXME: there is no way to recover from the Unhealthy state.
			d.Health = pluginapi.Unhealthy
			klog.Infof("'%s' device marked unhealthy: %s", plugin.rm.Resource(), d.ID)
			if err := s.Send(&pluginapi.ListAndWatchResponse{Devices: plugin.apiDevices()}); err != nil {
				return nil
			}
		}
	}
}

// GetPreferredAllocation returns the preferred allocation from the set of devices specified in the request
func (plugin *nvidiaDevicePlugin) GetPreferredAllocation(ctx context.Context, r *pluginapi.PreferredAllocationRequest) (*pluginapi.PreferredAllocationResponse, error) {
	response := &pluginapi.PreferredAllocationResponse{}
	for _, req := range r.ContainerRequests {
		devices, err := plugin.rm.GetPreferredAllocation(req.AvailableDeviceIDs, req.MustIncludeDeviceIDs, int(req.AllocationSize))
		if err != nil {
			return nil, fmt.Errorf("error getting list of preferred allocation devices: %v", err)
		}

		resp := &pluginapi.ContainerPreferredAllocationResponse{
			DeviceIDs: devices,
		}

		response.ContainerResponses = append(response.ContainerResponses, resp)
	}
	return response, nil
}

// Allocate returns a list of devices.
func (plugin *nvidiaDevicePlugin) Allocate(ctx context.Context, reqs *pluginapi.AllocateRequest) (*pluginapi.AllocateResponse, error) {
	if len(reqs.ContainerRequests) > 1 {
		return &pluginapi.AllocateResponse{}, errors.New("multiple Container Requests not supported")
	}
	responses := pluginapi.AllocateResponse{}
	if plugin.rm.Resource() != spec.ResourceName(util.ResourceName) {
		for range reqs.ContainerRequests {
			responses.ContainerResponses = append(responses.ContainerResponses, &pluginapi.ContainerAllocateResponse{})
		}
		return &responses, nil
	}
	nodeName := os.Getenv("NODE_NAME")
	gpuAmount := len(reqs.ContainerRequests[0].DevicesIds)
	current, err := util.GetPendingPod(nodeName, gpuAmount)
	if err != nil {
		nodelock.ReleaseNodeLock(nodeName, util.VGPUDeviceName)
		return &pluginapi.AllocateResponse{}, err
	}
	if current == nil {
		klog.Errorf("no pending pod found on node %s", nodeName)
		nodelock.ReleaseNodeLock(nodeName, util.VGPUDeviceName)
		return &pluginapi.AllocateResponse{}, errors.New("no pending pod found on node")
	}
	klog.V(3).InfoS("Current pending pod UID:", current.UID, "pod name", current.Name)

	for idx, req := range reqs.ContainerRequests {
		if strings.Contains(req.DevicesIds[0], "MIG") {
			if plugin.config.Sharing.TimeSlicing.FailRequestsGreaterThanOne && rm.AnnotatedIDs(req.DevicesIds).AnyHasAnnotations() {
				if len(req.DevicesIds) > 1 {
					util.PodAllocationFailed(nodeName, current)
					return nil, fmt.Errorf("request for '%v: %v' too large: maximum request size for shared resources is 1", plugin.rm.Resource(), len(req.DevicesIds))
				}
			}

			for _, id := range req.DevicesIds {
				if !plugin.rm.Devices().Contains(id) {
					util.PodAllocationFailed(nodeName, current)
					return nil, fmt.Errorf("invalid allocation request for '%s': unknown device: %s", plugin.rm.Resource(), id)
				}
			}

			response, err := plugin.getAllocateResponse(req.DevicesIds)
			if err != nil {
				util.PodAllocationFailed(nodeName, current)
				return nil, fmt.Errorf("failed to get allocate response: %v", err)
			}
			responses.ContainerResponses = append(responses.ContainerResponses, response)
		} else {

			currentCtr, devreq, err := util.GetNextDeviceRequest(util.NvidiaGPUDevice, *current)
			klog.V(4).InfoS("Selected Pod deviceAllocateFromAnnotation=", "request", devreq)
			//klog.V(4).InfoS("reqs device ids=", "deviceIDs", reqs.ContainerRequests[idx].DevicesIDs)
			if err != nil {
				klog.Errorln("get device from annotation failed", err.Error())
				util.PodAllocationFailed(nodeName, current)
				return &pluginapi.AllocateResponse{}, err
			}
			if len(devreq) != len(reqs.ContainerRequests[idx].DevicesIds) {
				klog.Errorln("device number not matched", devreq, reqs.ContainerRequests[idx].DevicesIds)
				util.PodAllocationFailed(nodeName, current)
				return &pluginapi.AllocateResponse{}, errors.New("device number not matched")
			}

			response, err := plugin.getAllocateResponse(plugin.GetContainerDeviceStrArray(devreq))
			if err != nil {
				return nil, fmt.Errorf("failed to get allocate response: %v", err)
			}
			err = util.EraseNextDeviceTypeFromAnnotation(util.NvidiaGPUDevice, *current)
			if err != nil {
				klog.Errorln("Erase annotation failed", err.Error())
				util.PodAllocationFailed(nodeName, current)
				return &pluginapi.AllocateResponse{}, err
			}

			if config.Mode != "mig" {
				for i, dev := range devreq {
					limitKey := fmt.Sprintf("CUDA_DEVICE_MEMORY_LIMIT_%v", i)
					response.Envs[limitKey] = fmt.Sprintf("%vm", dev.Usedmem*int32(config.GPUMemoryFactor))
				}
				response.Envs["CUDA_DEVICE_SM_LIMIT"] = fmt.Sprint(devreq[0].Usedcores)
				response.Envs["CUDA_DEVICE_MEMORY_SHARED_CACHE"] = fmt.Sprintf("/tmp/vgpu/%v.cache", uuid.New().String())

				if config.DeviceCoresScaling > 1 {
					response.Envs["CUDA_OVERSUBSCRIBE"] = "true"
				}
				if config.DisableCoreLimit {
					response.Envs[util.CoreLimitSwitch] = "disable"
				}

				hostHookPath := os.Getenv("HOOK_PATH")
				cacheFileHostDirectory := fmt.Sprintf("%s/vgpu/containers/%s_%s", hostHookPath, current.UID, currentCtr.Name)
				os.RemoveAll(cacheFileHostDirectory)

				os.MkdirAll(cacheFileHostDirectory, 0777)
				os.Chmod(cacheFileHostDirectory, 0777)
				os.MkdirAll("/tmp/vgpulock", 0777)
				os.Chmod("/tmp/vgpulock", 0777)

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
			responses.ContainerResponses = append(responses.ContainerResponses, response)
		}
	}
	klog.Infoln("Allocate Response", responses.ContainerResponses)
	util.PodAllocationTrySuccess(nodeName, current)
	return &responses, nil
}

func (plugin *nvidiaDevicePlugin) getAllocateResponse(requestIds []string) (*pluginapi.ContainerAllocateResponse, error) {
	deviceIDs := plugin.uniqueDeviceIDsFromAnnotatedDeviceIDs(requestIds)

	// Create an empty response that will be updated as required below.
	response := &pluginapi.ContainerAllocateResponse{
		Envs: make(map[string]string),
	}
	if plugin.deviceListStrategies.AnyCDIEnabled() {
		responseID := uuid.New().String()
		if err := plugin.updateResponseForCDI(response, responseID, deviceIDs...); err != nil {
			return nil, fmt.Errorf("failed to get allocate response for CDI: %v", err)
		}
	}
	if plugin.mps.enabled {
		plugin.updateResponseForMPS(response)
	}

	if plugin.config.Flags.GDRCopyEnabled != nil && *plugin.config.Flags.GDRCopyEnabled {
		response.Envs["NVIDIA_GDRCOPY"] = "enabled"
	}
	if plugin.config.Flags.GDSEnabled != nil && *plugin.config.Flags.GDSEnabled {
		response.Envs["NVIDIA_GDS"] = "enabled"
	}
	if plugin.config.Flags.MOFEDEnabled != nil && *plugin.config.Flags.MOFEDEnabled {
		response.Envs["NVIDIA_MOFED"] = "enabled"
	}

	// The following modifications are only made if at least one non-CDI device
	// list strategy is selected.
	if plugin.deviceListStrategies.AllCDIEnabled() {
		return response, nil
	}

	if plugin.deviceListStrategies.Includes(spec.DeviceListStrategyEnvVar) {
		plugin.updateResponseForDeviceListEnvVar(response, deviceIDs...)
		plugin.updateResponseForImexChannelsEnvVar(response)
	}
	if plugin.deviceListStrategies.Includes(spec.DeviceListStrategyVolumeMounts) {
		plugin.updateResponseForDeviceMounts(response, deviceIDs...)
	}
	if plugin.config.Flags.Plugin.PassDeviceSpecs != nil && *plugin.config.Flags.Plugin.PassDeviceSpecs {
		response.Devices = append(response.Devices, plugin.apiDeviceSpecs(*plugin.config.Flags.NvidiaDevRoot, requestIds)...)
	}
	return response, nil
}

// updateResponseForMPS ensures that the ContainerAllocate response contains the information required to use MPS.
// This includes per-resource pipe and log directories as well as a global daemon-specific shm
// and assumes that an MPS control daemon has already been started.
func (plugin nvidiaDevicePlugin) updateResponseForMPS(response *pluginapi.ContainerAllocateResponse) {
	plugin.mps.updateReponse(response)
}

// updateResponseForCDI updates the specified response for the given device IDs.
// This response contains the annotations required to trigger CDI injection in the container engine or nvidia-container-runtime.
func (plugin *nvidiaDevicePlugin) updateResponseForCDI(response *pluginapi.ContainerAllocateResponse, responseID string, deviceIDs ...string) error {
	var devices []string
	for _, id := range deviceIDs {
		devices = append(devices, plugin.cdiHandler.QualifiedName("gpu", id))
	}
	for _, channel := range plugin.imexChannels {
		devices = append(devices, plugin.cdiHandler.QualifiedName("imex-channel", channel.ID))
	}

	devices = append(devices, plugin.cdiHandler.AdditionalDevices()...)

	if len(devices) == 0 {
		return nil
	}

	if plugin.deviceListStrategies.Includes(spec.DeviceListStrategyCDIAnnotations) {
		annotations, err := plugin.getCDIDeviceAnnotations(responseID, devices...)
		if err != nil {
			return err
		}
		response.Annotations = annotations
	}
	if plugin.deviceListStrategies.Includes(spec.DeviceListStrategyCDICRI) {
		for _, device := range devices {
			cdiDevice := pluginapi.CDIDevice{
				Name: device,
			}
			response.CdiDevices = append(response.CdiDevices, &cdiDevice)
		}
	}

	return nil
}

func (plugin *nvidiaDevicePlugin) getCDIDeviceAnnotations(id string, devices ...string) (map[string]string, error) {
	annotations, err := cdiapi.UpdateAnnotations(map[string]string{}, "nvidia-device-plugin", id, devices)
	if err != nil {
		return nil, fmt.Errorf("failed to add CDI annotations: %v", err)
	}

	if plugin.cdiAnnotationPrefix == spec.DefaultCDIAnnotationPrefix {
		return annotations, nil
	}

	// update annotations if a custom CDI prefix is configured
	updatedAnnotations := make(map[string]string)
	for k, v := range annotations {
		newKey := plugin.cdiAnnotationPrefix + strings.TrimPrefix(k, spec.DefaultCDIAnnotationPrefix)
		updatedAnnotations[newKey] = v
	}

	return updatedAnnotations, nil
}

// PreStartContainer is unimplemented for this plugin
func (plugin *nvidiaDevicePlugin) PreStartContainer(context.Context, *pluginapi.PreStartContainerRequest) (*pluginapi.PreStartContainerResponse, error) {
	return &pluginapi.PreStartContainerResponse{}, nil
}

// dial establishes the gRPC communication with the registered device plugin.
func (plugin *nvidiaDevicePlugin) dial(unixSocketPath string, timeout time.Duration) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(plugin.ctx, timeout)
	defer cancel()
	//nolint:staticcheck  // TODO: Switch to grpc.NewClient
	c, err := grpc.DialContext(ctx, unixSocketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		//nolint:staticcheck  // TODO: WithBlock is deprecated.
		grpc.WithBlock(),
		grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", addr)
		}),
	)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (plugin *nvidiaDevicePlugin) uniqueDeviceIDsFromAnnotatedDeviceIDs(ids []string) []string {
	var deviceIDs []string
	if *plugin.config.Flags.Plugin.DeviceIDStrategy == spec.DeviceIDStrategyUUID {
		deviceIDs = rm.AnnotatedIDs(ids).GetIDs()
	}
	if *plugin.config.Flags.Plugin.DeviceIDStrategy == spec.DeviceIDStrategyIndex {
		deviceIDs = plugin.rm.Devices().Subset(ids).GetIndices()
	}
	var uniqueIDs []string
	seen := make(map[string]bool)
	for _, id := range deviceIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		uniqueIDs = append(uniqueIDs, id)
	}
	return uniqueIDs
}

func (plugin *nvidiaDevicePlugin) apiDevices() []*pluginapi.Device {
	devs := plugin.rm.Devices().GetPluginDevices()
	/*if strings.Compare(plugin.migStrategy, "mixed") == 0 {
		return devs
	}*/
	var res []*pluginapi.Device

	if plugin.rm.Resource() == spec.ResourceName(util.ResourceMem) {
		for _, dev := range devs {
			ndev, ret := config.Nvml().DeviceGetHandleByUUID(dev.ID)
			if ret != nvml.SUCCESS {
				fmt.Println("nvml new device by uuid error id=", dev.ID)
				panic(ret)
			}

			memory, ret := config.Nvml().DeviceGetMemoryInfo(ndev)
			if ret != nvml.SUCCESS {
				fmt.Println("failed to get memory info for device id=", dev.ID)
				panic(ret)
			}
			registeredmem := int(memory.Total/(1024*1024)) / int(config.GPUMemoryFactor)
			i := 0
			klog.Infoln("memory=", registeredmem, "id=", dev.ID)
			for i < registeredmem {
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
	} else if plugin.rm.Resource() == spec.ResourceName(util.ResourceCores) {
		for _, dev := range devs {
			coresNum := int(100 * config.DeviceCoresScaling)
			i := 0
			for i < coresNum {
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

	for _, dev := range devs {
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

// updateResponseForDeviceListEnvVar sets the environment variable for the requested devices.
func (plugin *nvidiaDevicePlugin) updateResponseForDeviceListEnvVar(response *pluginapi.ContainerAllocateResponse, deviceIDs ...string) {
	response.Envs[deviceListEnvVar] = strings.Join(deviceIDs, ",")
}

// updateResponseForImexChannelsEnvVar sets the environment variable for the requested IMEX channels.
func (plugin *nvidiaDevicePlugin) updateResponseForImexChannelsEnvVar(response *pluginapi.ContainerAllocateResponse) {
	var channelIDs []string
	for _, channel := range plugin.imexChannels {
		channelIDs = append(channelIDs, channel.ID)
	}
	if len(channelIDs) > 0 {
		response.Envs[spec.ImexChannelEnvVar] = strings.Join(channelIDs, ",")
	}
}

// updateResponseForDeviceMounts sets the mounts required to request devices if volume mounts are used.
func (plugin *nvidiaDevicePlugin) updateResponseForDeviceMounts(response *pluginapi.ContainerAllocateResponse, deviceIDs ...string) {
	plugin.updateResponseForDeviceListEnvVar(response, deviceListAsVolumeMountsContainerPathRoot)

	for _, id := range deviceIDs {
		mount := &pluginapi.Mount{
			HostPath:      deviceListAsVolumeMountsHostPath,
			ContainerPath: filepath.Join(deviceListAsVolumeMountsContainerPathRoot, id),
		}
		response.Mounts = append(response.Mounts, mount)
	}
	for _, channel := range plugin.imexChannels {
		mount := &pluginapi.Mount{
			HostPath:      deviceListAsVolumeMountsHostPath,
			ContainerPath: filepath.Join(deviceListAsVolumeMountsContainerPathRoot, "imex", channel.ID),
		}
		response.Mounts = append(response.Mounts, mount)
	}
}

func (plugin *nvidiaDevicePlugin) apiDeviceSpecs(devRoot string, ids []string) []*pluginapi.DeviceSpec {
	optional := map[string]bool{
		"/dev/nvidiactl":        true,
		"/dev/nvidia-uvm":       true,
		"/dev/nvidia-uvm-tools": true,
		"/dev/nvidia-modeset":   true,
	}

	paths := plugin.rm.GetDevicePaths(ids)

	var specs []*pluginapi.DeviceSpec
	for _, p := range paths {
		if optional[p] {
			if _, err := os.Stat(p); err != nil {
				continue
			}
		}
		spec := &pluginapi.DeviceSpec{
			ContainerPath: p,
			HostPath:      filepath.Join(devRoot, p),
			Permissions:   "rw",
		}
		specs = append(specs, spec)
	}

	for _, channel := range plugin.imexChannels {
		spec := &pluginapi.DeviceSpec{
			ContainerPath: channel.Path,
			// TODO: The HostPath property for a channel is not the correct value to use here.
			// The `devRoot` there represents the devRoot in the current container when discovering devices
			// and is set to "{{ .*config.Flags.Plugin.ContainerDriverRoot }}/dev".
			// The devRoot in this context is the {{ .config.Flags.NvidiaDevRoot }} and defines the
			// root for device nodes on the host. This is usually / or /run/nvidia/driver when the
			// driver container is used.
			HostPath:    filepath.Join(devRoot, channel.Path),
			Permissions: "rw",
		}
		specs = append(specs, spec)
	}

	return specs
}

func (plugin *nvidiaDevicePlugin) WatchAndRegister() {
	klog.Infof("into WatchAndRegister")
	for {
		if len(config.Mode) == 0 {
			klog.V(5).Info("register skipped, waiting for device config to be loaded")
			time.Sleep(time.Second * 2)
			continue
		}
		err := RegisterInAnnotation(plugin.rm.Devices().GetPluginDevices())
		if err != nil {
			klog.Errorf("register error, %v", err)
			time.Sleep(time.Second * 5)
		} else {
			time.Sleep(time.Second * 30)
		}
	}
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

func (plugin *nvidiaDevicePlugin) ApplyMigTemplate() {
	data, err := yaml.Marshal(plugin.migCurrent)
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

func (plugin *nvidiaDevicePlugin) GetContainerDeviceStrArray(c util.ContainerDevices) []string {
	tmp := []string{}
	needsreset := false
	position := 0
	for _, val := range c {
		if !strings.Contains(val.UUID, "[") {
			tmp = append(tmp, val.UUID)
		} else {
			devtype, devindex := util.GetIndexAndTypeFromUUID(val.UUID)
			position, needsreset = plugin.GenerateMigTemplate(devtype, devindex, val)
			if needsreset {
				plugin.ApplyMigTemplate()
			}
			tmp = append(tmp, util.GetMigUUIDFromIndex(val.UUID, position))
		}
	}
	klog.V(3).Infoln("mig current=", plugin.migCurrent, ":", needsreset, "position=", position, "uuid lists", tmp)
	return tmp
}

func (plugin *nvidiaDevicePlugin) GenerateMigTemplate(devtype string, devindex int, val util.ContainerDevice) (int, bool) {
	needsreset := false
	position := -1 // Initialize to an invalid position

	for _, migTemplate := range config.SchedulerConfig.MigGeometriesList {
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

			for migidx, migpartedDev := range plugin.migCurrent.MigConfigs["current"] {
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
						for k := range plugin.migCurrent.MigConfigs["current"][migidx].MigDevices {
							delete(plugin.migCurrent.MigConfigs["current"][migidx].MigDevices, k)
						}

						for _, migTemplateEntry := range v {
							plugin.migCurrent.MigConfigs["current"][migidx].MigDevices[migTemplateEntry.Name] = migTemplateEntry.Count
							plugin.migCurrent.MigConfigs["current"][migidx].MigEnabled = true
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

// Helper function to deepcopy new mig spec
func deepCopyMigConfig(src config.MigConfigSpec) config.MigConfigSpec {
	dst := src
	if src.Devices != nil {
		dst.Devices = make([]int32, len(src.Devices))
		copy(dst.Devices, src.Devices)
	}
	if src.MigDevices != nil {
		dst.MigDevices = make(map[string]int32)
		for k, v := range src.MigDevices {
			dst.MigDevices[k] = v
		}
	}
	return dst
}

func (plugin *nvidiaDevicePlugin) processMigConfigs(migConfigs map[string]config.MigConfigSpecSlice, deviceCount int) (config.MigConfigSpecSlice, error) {
	if migConfigs == nil {
		return nil, fmt.Errorf("migConfigs cannot be nil")
	}
	if deviceCount <= 0 {
		return nil, fmt.Errorf("deviceCount must be positive")
	}

	transformConfigs := func() (config.MigConfigSpecSlice, error) {
		var result config.MigConfigSpecSlice

		if len(migConfigs["current"]) == 1 && len(migConfigs["current"][0].Devices) == 0 {
			for i := 0; i < deviceCount; i++ {
				config := deepCopyMigConfig(migConfigs["current"][0])
				config.Devices = []int32{int32(i)}
				result = append(result, config)
			}
			return result, nil
		}

		deviceToConfig := make(map[int32]*config.MigConfigSpec)
		for i := range migConfigs["current"] {
			for _, device := range migConfigs["current"][i].Devices {
				deviceToConfig[device] = &migConfigs["current"][i]
			}
		}

		for i := 0; i < deviceCount; i++ {
			deviceIndex := int32(i)
			config, exists := deviceToConfig[deviceIndex]
			if !exists {
				return nil, fmt.Errorf("device %d does not match any MIG configuration", i)
			}
			newConfig := deepCopyMigConfig(*config)
			newConfig.Devices = []int32{deviceIndex}
			result = append(result, newConfig)

		}
		return result, nil
	}

	return transformConfigs()
}
