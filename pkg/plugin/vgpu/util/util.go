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

package util

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"gopkg.in/yaml.v2"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
	"volcano.sh/k8s-device-plugin/pkg/lock"
	"volcano.sh/k8s-device-plugin/pkg/plugin/vgpu/config"
)

var DevicesToHandle []string

func init() {
	client, _ := lock.NewClient()
	lock.UseClient(client)
	DevicesToHandle = []string{}
	DevicesToHandle = append(DevicesToHandle, NvidiaGPUCommonWord)
}

func GlobalFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	fs.StringVar(&ResourceName, "resource-name", "volcano.sh/vgpu-number", "resource name")
	fs.StringVar(&ResourceMem, "resource-memory-name", "volcano.sh/vgpu-memory", "resource name for resource memory resources")
	fs.StringVar(&ResourceCores, "resource-core-name", "volcano.sh/vgpu-cores", "resource name for resource core resources")
	fs.BoolVar(&DebugMode, "debug", false, "debug mode")
	klog.InitFlags(fs)
	return fs
}

func GetNode(nodename string) (*v1.Node, error) {
	n, err := lock.GetClient().CoreV1().Nodes().Get(context.Background(), nodename, metav1.GetOptions{})
	return n, err
}

func GetPendingPod(node string) (*v1.Pod, error) {
	podList, err := lock.GetClient().CoreV1().Pods("").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	oldestPod := getOldestPod(podList.Items, node)
	if oldestPod == nil {
		return nil, fmt.Errorf("cannot get valid pod")
	}

	return oldestPod, nil
}

func getOldestPod(pods []v1.Pod, nodename string) *v1.Pod {
	if len(pods) == 0 {
		return nil
	}
	oldest := pods[0]
	for _, pod := range pods {
		if pod.Annotations[AssignedNodeAnnotations] == nodename {
			klog.V(4).Infof("pod %s, predicate time: %s", pod.Name, pod.Annotations[AssignedTimeAnnotations])
			if getPredicateTimeFromPodAnnotation(&oldest) > getPredicateTimeFromPodAnnotation(&pod) {
				oldest = pod
			}
		}
	}
	klog.V(4).Infof("oldest pod %#v, predicate time: %#v", oldest.Name,
		oldest.Annotations[AssignedTimeAnnotations])
	annotation := map[string]string{AssignedTimeAnnotations: strconv.FormatUint(math.MaxUint64, 10)}
	if err := PatchPodAnnotations(&oldest, annotation); err != nil {
		klog.Errorf("update pod %s failed, err: %v", oldest.Name, err)
		return nil
	}
	return &oldest
}

func getPredicateTimeFromPodAnnotation(pod *v1.Pod) uint64 {
	assumeTimeStr, ok := pod.Annotations[AssignedTimeAnnotations]
	if !ok {
		klog.Warningf("volcano not write timestamp, pod Name: %s", pod.Name)
		return math.MaxUint64
	}
	if len(assumeTimeStr) > PodAnnotationMaxLength {
		klog.Warningf("timestamp fmt invalid, pod Name: %s", pod.Name)
		return math.MaxUint64
	}
	predicateTime, err := strconv.ParseUint(assumeTimeStr, 10, 64)
	if err != nil {
		klog.Errorf("parse timestamp failed, %v", err)
		return math.MaxUint64
	}
	return predicateTime
}

func DecodeNodeDevices(str string) []*DeviceInfo {
	if !strings.Contains(str, ":") {
		return []*DeviceInfo{}
	}
	tmp := strings.Split(str, ":")
	var retval []*DeviceInfo
	for _, val := range tmp {
		if strings.Contains(val, ",") {
			items := strings.Split(val, ",")
			count, _ := strconv.Atoi(items[1])
			devmem, _ := strconv.Atoi(items[2])
			health, _ := strconv.ParseBool(items[4])
			i := DeviceInfo{
				Id:     items[0],
				Count:  int32(count),
				Devmem: int32(devmem),
				Type:   items[3],
				Health: health,
			}
			retval = append(retval, &i)
		}
	}
	return retval
}

func EncodeNodeDevices(dlist []*DeviceInfo) string {
	tmp := ""
	for _, val := range dlist {
		tmp += val.Id + "," + strconv.FormatInt(int64(val.Count), 10) + "," + strconv.Itoa(int(val.Devmem)) + "," + val.Type + "," + strconv.FormatBool(val.Health) + "," + val.Mode + ":"
	}
	klog.V(3).Infoln("Encoded node Devices", tmp)
	return tmp
}

func EncodeContainerDevices(cd ContainerDevices) string {
	tmp := ""
	for _, val := range cd {
		tmp += val.UUID + "," + val.Type + "," + strconv.Itoa(int(val.Usedmem)) + "," + strconv.Itoa(int(val.Usedcores)) + ":"
	}
	fmt.Println("Encoded container Devices=", tmp)
	return tmp
	//return strings.Join(cd, ",")
}

func EncodePodDevices(pd PodDevices) string {
	var ss []string
	for _, cd := range pd {
		ss = append(ss, EncodeContainerDevices(cd))
	}
	return strings.Join(ss, ";")
}

func DecodeContainerDevices(str string) ContainerDevices {
	if len(str) == 0 {
		return ContainerDevices{}
	}
	cd := strings.Split(str, ":")
	contdev := ContainerDevices{}
	tmpdev := ContainerDevice{}
	if len(str) == 0 {
		return contdev
	}
	for _, val := range cd {
		if strings.Contains(val, ",") {
			tmpstr := strings.Split(val, ",")
			tmpdev.UUID = tmpstr[0]
			tmpdev.Type = tmpstr[1]
			devmem, _ := strconv.ParseInt(tmpstr[2], 10, 32)
			tmpdev.Usedmem = int32(devmem)
			devcores, _ := strconv.ParseInt(tmpstr[3], 10, 32)
			tmpdev.Usedcores = int32(devcores)
			contdev = append(contdev, tmpdev)
		}
	}
	return contdev
}

func DecodePodDevices(str string) PodDevices {
	if len(str) == 0 {
		return PodDevices{}
	}
	var pd PodDevices
	for _, s := range strings.Split(str, ";") {
		cd := DecodeContainerDevices(s)
		pd = append(pd, cd)
	}
	return pd
}

func GetNextDeviceRequest(dtype string, p v1.Pod) (v1.Container, ContainerDevices, error) {
	pdevices := DecodePodDevices(p.Annotations[AssignedIDsToAllocateAnnotations])
	klog.Infoln("pdevices=", pdevices)
	res := ContainerDevices{}
	for idx, val := range pdevices {
		found := false
		for _, dev := range val {
			if strings.Compare(dtype, dev.Type) == 0 {
				res = append(res, dev)
				found = true
			}
		}
		if found {
			return p.Spec.Containers[idx], res, nil
		}
	}
	return v1.Container{}, res, errors.New("device request not found")
}

func EraseNextDeviceTypeFromAnnotation(dtype string, p v1.Pod) error {
	pdevices := DecodePodDevices(p.Annotations[AssignedIDsToAllocateAnnotations])
	res := PodDevices{}
	found := false
	for _, val := range pdevices {
		if found {
			res = append(res, val)
			continue
		} else {
			tmp := ContainerDevices{}
			for _, dev := range val {
				if strings.Compare(dtype, dev.Type) == 0 {
					found = true
				} else {
					tmp = append(tmp, dev)
				}
			}
			if !found {
				res = append(res, val)
			} else {
				res = append(res, tmp)
			}
		}
	}
	klog.Infoln("After erase res=", res)
	newannos := make(map[string]string)
	newannos[AssignedIDsToAllocateAnnotations] = EncodePodDevices(res)
	return PatchPodAnnotations(&p, newannos)
}

func PodAllocationTrySuccess(nodeName string, pod *v1.Pod) {
	refreshed, _ := lock.GetClient().CoreV1().Pods(pod.Namespace).Get(context.Background(), pod.Name, metav1.GetOptions{})
	annos := refreshed.Annotations[AssignedIDsToAllocateAnnotations]
	klog.Infoln("TrySuccess:", annos)
	for _, val := range DevicesToHandle {
		if strings.Contains(annos, val) {
			return
		}
	}
	klog.Infoln("AllDevicesAllocateSuccess releasing lock")
	PodAllocationSuccess(nodeName, pod)
}

func PodAllocationSuccess(nodeName string, pod *v1.Pod) {
	newannos := make(map[string]string)
	newannos[DeviceBindPhase] = DeviceBindSuccess
	err := PatchPodAnnotations(pod, newannos)
	if err != nil {
		klog.Errorf("patchPodAnnotations failed:%v", err.Error())
	}
	err = lock.ReleaseNodeLock(nodeName, VGPUDeviceName)
	if err != nil {
		klog.Errorf("release lock failed:%v", err.Error())
	}
}

func PodAllocationFailed(nodeName string, pod *v1.Pod) {
	newannos := make(map[string]string)
	newannos[DeviceBindPhase] = DeviceBindFailed
	err := PatchPodAnnotations(pod, newannos)
	if err != nil {
		klog.Errorf("patchPodAnnotations failed:%v", err.Error())
	}
	err = lock.ReleaseNodeLock(nodeName, VGPUDeviceName)
	if err != nil {
		klog.Errorf("release lock failed:%v", err.Error())
	}
}

func PatchNodeAnnotations(node *v1.Node, annotations map[string]string) error {
	type patchMetadata struct {
		Annotations map[string]string `json:"annotations,omitempty"`
	}
	type patchPod struct {
		Metadata patchMetadata `json:"metadata"`
		//Spec     patchSpec     `json:"spec,omitempty"`
	}

	p := patchPod{}
	p.Metadata.Annotations = annotations

	bytes, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = lock.GetClient().CoreV1().Nodes().
		Patch(context.Background(), node.Name, k8stypes.StrategicMergePatchType, bytes, metav1.PatchOptions{})
	if err != nil {
		klog.Infof("patch pod %v failed, %v", node.Name, err)
	}
	return err
}

func PatchPodAnnotations(pod *v1.Pod, annotations map[string]string) error {
	type patchMetadata struct {
		Annotations map[string]string `json:"annotations,omitempty"`
	}
	type patchPod struct {
		Metadata patchMetadata `json:"metadata"`
		//Spec     patchSpec     `json:"spec,omitempty"`
	}

	p := patchPod{}
	p.Metadata.Annotations = annotations

	bytes, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = lock.GetClient().CoreV1().Pods(pod.Namespace).
		Patch(context.Background(), pod.Name, k8stypes.StrategicMergePatchType, bytes, metav1.PatchOptions{})
	if err != nil {
		klog.Infof("patch pod %v failed, %v", pod.Name, err)
	}
	return err
}

func LoadConfigFromCM(cmName string) (*config.Config, error) {
	lock.NewClient()
	cm, err := lock.GetClient().CoreV1().ConfigMaps("kube-system").Get(context.Background(), cmName, metav1.GetOptions{})
	if err != nil {
		cm, err = lock.GetClient().CoreV1().ConfigMaps("volcano-system").Get(context.Background(), cmName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
	}
	data, ok := cm.Data[DeviceConfigurationConfigMapKey]
	if !ok {
		return nil, fmt.Errorf("%v not found in ConfigMap %v", DeviceConfigurationConfigMapKey, cmName)
	}
	var yamlData config.Config
	err = yaml.Unmarshal([]byte(data), &yamlData)
	if err != nil {
		return nil, err
	}
	return &yamlData, nil
}

func LoadConfig(path string) (*config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var yamlData config.Config
	err = yaml.Unmarshal(data, &yamlData)
	if err != nil {
		return nil, err
	}
	return &yamlData, nil
}

func GenerateVirtualDeviceID(id uint, fakeCounter uint) string {
	return fmt.Sprintf("%d-%d", id, fakeCounter)
}

// GetDevices returns virtual devices and all physical devices by index.
func GetDevices(gpuMemoryFactor uint) ([]*pluginapi.Device, map[uint]string) {
	n, ret := config.Nvml().DeviceGetCount()
	if ret != nvml.SUCCESS {
		klog.Fatalf("call nvml.DeviceGetCount with error: %v", ret)
	}

	var virtualDevs []*pluginapi.Device
	deviceByIndex := map[uint]string{}
	for i := uint(0); i < uint(n); i++ {
		d, ret := config.Nvml().DeviceGetHandleByIndex(int(i))
		if ret != nvml.SUCCESS {
			klog.Fatalf("call nvml.DeviceGetHandleByIndex with error: %v", ret)
		}
		uuid, ret := d.GetUUID()
		if ret != nvml.SUCCESS {
			klog.Fatalf("call GetUUID with error: %v", ret)
		}
		id := i
		deviceByIndex[id] = uuid
		memory, ret := d.GetMemoryInfo()
		if ret != nvml.SUCCESS {
			klog.Fatalf("call GetMemoryInfo with error: %v", ret)
		}
		deviceGPUMemory := uint(memory.Total / (1024 * 1024))
		for j := uint(0); j < deviceGPUMemory/gpuMemoryFactor; j++ {
			klog.V(4).Infof("adding virtual device: %d", j)
			fakeID := GenerateVirtualDeviceID(id, j)
			virtualDevs = append(virtualDevs, &pluginapi.Device{
				ID:     fakeID,
				Health: pluginapi.Healthy,
			})
		}
	}

	return virtualDevs, deviceByIndex
}

func GetDeviceNums() int {
	count, ret := config.Nvml().DeviceGetCount()
	if ret != nvml.SUCCESS {
		klog.Error(`nvml get count error ret=`, ret)
	}
	return count
}

func GetIndexAndTypeFromUUID(uuid string) (string, int) {
	originuuid := strings.Split(uuid, "[")[0]
	ndev, ret := config.Nvml().DeviceGetHandleByUUID(originuuid)
	if ret != nvml.SUCCESS {
		klog.Error("nvml get handlebyuuid error ret=", ret)
		panic(0)
	}
	model, ret := ndev.GetName()
	if ret != nvml.SUCCESS {
		klog.Error("nvml get name error ret=", ret)
		panic(0)
	}
	index, ret := ndev.GetIndex()
	if ret != nvml.SUCCESS {
		klog.Error("nvml get index error ret=", ret)
		panic(0)
	}
	return model, index
}

func GetMigUUIDFromIndex(uuid string, idx int) string {
	originuuid := strings.Split(uuid, "[")[0]
	ndev, ret := config.Nvml().DeviceGetHandleByUUID(originuuid)
	if ret != nvml.SUCCESS {
		klog.Error(`nvml get device uuid error ret=`, ret)
		panic(0)
	}
	migdev, ret := config.Nvml().DeviceGetMigDeviceHandleByIndex(ndev, idx)
	if ret != nvml.SUCCESS {
		klog.Error("nvml get mig dev error ret=", ret, ",idx=", idx, "using nvidia-smi -L for query")
		cmd := exec.Command("nvidia-smi", "-L")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil {
			klog.Fatalf("nvidia-smi -L failed with %s\n", err)
		}
		outStr := stdout.String()
		uuid := GetMigUUIDFromSmiOutput(outStr, originuuid, idx)
		return uuid
	}
	res, ret := migdev.GetUUID()
	if ret != nvml.SUCCESS {
		klog.Error(`nvml get mig uuid error ret=`, ret)
		panic(0)
	}
	return res
}

func GetMigUUIDFromSmiOutput(output string, uuid string, idx int) string {
	migmode := false
	for _, val := range strings.Split(output, "\n") {
		if !strings.Contains(val, "MIG") && strings.Contains(val, uuid) {
			migmode = true
			continue
		}
		if !strings.Contains(val, "MIG") && !strings.Contains(val, uuid) {
			migmode = false
			continue
		}
		if !migmode {
			continue
		}
		klog.Infoln("inspecting", val)
		num := strings.Split(val, "Device")[1]
		num = strings.Split(num, ":")[0]
		num = strings.TrimSpace(num)
		index, err := strconv.Atoi(num)
		if err != nil {
			klog.Fatal("atoi failed num=", num)
		}
		if index == idx {
			outputStr := strings.Split(val, ":")[2]
			outputStr = strings.TrimSpace(outputStr)
			outputStr = strings.TrimRight(outputStr, ")")
			return outputStr
		}
	}
	return ""
}

// Enhanced ExtractMigTemplatesFromUUID with error handling.
func ExtractMigTemplatesFromUUID(uuid string) (string, int, error) {
	parts := strings.Split(uuid, "[")
	if len(parts) < 2 {
		return "", -1, fmt.Errorf("invalid UUID format: missing '[' delimiter")
	}

	tmp := parts[1]
	parts = strings.Split(tmp, "]")
	if len(parts) < 2 {
		return "", -1, fmt.Errorf("invalid UUID format: missing ']' delimiter")
	}

	tmp = parts[0]
	parts = strings.Split(tmp, "-")
	if len(parts) < 2 {
		return "", -1, fmt.Errorf("invalid UUID format: missing '-' delimiter")
	}

	templateGroupName := strings.TrimSpace(parts[0])
	if len(templateGroupName) == 0 {
		return "", -1, fmt.Errorf("invalid UUID format: missing template group name")
	}

	pos, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", -1, fmt.Errorf("invalid position: %v", err)
	}

	return templateGroupName, pos, nil
}

func LoadNvidiaConfig() *config.NvidiaConfig {
	configs, err := LoadConfigFromCM("volcano-vgpu-device-config")
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
	if err := readFromConfigFile(&nvidiaConfig); err != nil {
		klog.InfoS("readFrom device cm error", err.Error())
	}
	klog.Infoln("Loaded config=", nvidiaConfig)
	return &nvidiaConfig
}

func readFromConfigFile(sConfig *config.NvidiaConfig) error {
	config.Mode = "hami-core"
	jsonbyte, err := os.ReadFile("/config/config.json")
	if err != nil {
		return err
	}
	var deviceConfigs config.DevicePluginConfigs
	err = json.Unmarshal(jsonbyte, &deviceConfigs)
	if err != nil {
		return err
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
				config.DevicePluginFilterDevice = val.FilterDevice
			}
			if len(val.OperatingMode) > 0 {
				config.Mode = val.OperatingMode
			}
			klog.Infof("FilterDevice: %v", val.FilterDevice)
		}
	}
	return nil
}
