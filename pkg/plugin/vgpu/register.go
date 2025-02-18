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
	"fmt"
	"strings"
	"time"

	"github.com/NVIDIA/gpu-monitoring-tools/bindings/go/nvml"
	"k8s.io/klog/v2"
	"volcano.sh/k8s-device-plugin/pkg/plugin/vgpu/config"
	"volcano.sh/k8s-device-plugin/pkg/plugin/vgpu/util"
)

type DevListFunc func() []*Device

type DeviceRegister struct {
	deviceCache *DeviceCache
	unhealthy   chan *Device
	stopCh      chan struct{}
}

func NewDeviceRegister(deviceCache *DeviceCache) *DeviceRegister {
	return &DeviceRegister{
		deviceCache: deviceCache,
		unhealthy:   make(chan *Device),
		stopCh:      make(chan struct{}),
	}
}

func (r *DeviceRegister) Start() {
	r.deviceCache.AddNotifyChannel("register", r.unhealthy)
	go r.WatchAndRegister()
}

func (r *DeviceRegister) Stop() {
	close(r.stopCh)
}

func (r *DeviceRegister) apiDevices() *[]*util.DeviceInfo {
	devs := r.deviceCache.GetCache()
	res := make([]*util.DeviceInfo, 0, len(devs))
	for _, dev := range devs {
		ndev, err := nvml.NewDeviceByUUID(dev.ID)
		if err != nil {
			fmt.Println("nvml new device by uuid error id=", dev.ID)
			panic(0)
		} else {
			klog.V(3).Infoln("nvml registered device id=", dev.ID, "memory=", *ndev.Memory, "type=", *ndev.Model)
		}
		registeredmem := int32(*ndev.Memory) / int32(config.GPUMemoryFactor)
		klog.V(3).Infoln("GPUMemoryFactor=", config.GPUMemoryFactor, "registeredmem=", registeredmem)
		res = append(res, &util.DeviceInfo{
			Id:     dev.ID,
			Count:  int32(config.DeviceSplitCount),
			Devmem: registeredmem,
			Mode:   config.Mode,
			Type:   fmt.Sprintf("%v-%v", "NVIDIA", *ndev.Model),
			Health: strings.EqualFold(dev.Health, "healthy"),
		})
	}
	return &res
}

func (r *DeviceRegister) RegisterInAnnotation() error {
	devices := r.apiDevices()
	annos := make(map[string]string)
	node, err := util.GetNode(config.NodeName)
	if err != nil {
		klog.Errorln("get node error", err.Error())
		return err
	}
	encodeddevices := util.EncodeNodeDevices(*devices)
	annos[util.NodeHandshake] = "Reported " + time.Now().String()
	annos[util.NodeNvidiaDeviceRegistered] = encodeddevices
	klog.Infoln("Reporting devices", encodeddevices, "in", time.Now().String())
	err = util.PatchNodeAnnotations(node, annos)

	if err != nil {
		klog.Errorln("patch node error", err.Error())
	}
	return err
}

func (r *DeviceRegister) WatchAndRegister() {
	klog.Infof("into WatchAndRegister")
	for {
		err := r.RegisterInAnnotation()
		if err != nil {
			klog.Errorf("register error, %v", err)
			time.Sleep(time.Second * 5)
		} else {
			time.Sleep(time.Second * 30)
		}
	}
}
