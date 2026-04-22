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
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/util"
)

var (
	nodeName = flag.String("node_name", os.Getenv("NODE_NAME"), "node name")
)

func RegisterInAnnotation(devs []*pluginapi.Device) error {
	devices := ConvertDeviceInfo(devs)
	annos := make(map[string]string)
	node, err := util.GetNode(*nodeName)
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

func ConvertDeviceInfo(devs []*pluginapi.Device) *[]*util.DeviceInfo {
	res := make([]*util.DeviceInfo, 0, len(devs))
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

		model, ret := config.Nvml().DeviceGetName(ndev)
		if ret != nvml.SUCCESS {
			fmt.Println("failed to get model name for device id=", dev.ID)
			panic(ret)
		}

		klog.V(3).Infoln("nvml registered device id=", dev.ID, "memory=", memory.Total, "type=", model)

		registeredmem := int32(memory.Total/(1024*1024)) / int32(config.GPUMemoryFactor)
		klog.V(3).Infoln("GPUMemoryFactor=", config.GPUMemoryFactor, "registeredmem=", registeredmem)
		res = append(res, &util.DeviceInfo{
			Id:     dev.ID,
			Count:  int32(config.DeviceSplitCount),
			Devmem: registeredmem,
			Mode:   config.Mode,
			Type:   fmt.Sprintf("%v-%v", "NVIDIA", model),
			Health: strings.EqualFold(dev.Health, "healthy"),
		})
	}
	return &res
}
