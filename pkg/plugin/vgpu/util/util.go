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
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"volcano.sh/k8s-device-plugin/pkg/lock"
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
		tmp += val.Id + "," + strconv.FormatInt(int64(val.Count), 10) + "," + strconv.Itoa(int(val.Devmem)) + "," + val.Type + "," + strconv.FormatBool(val.Health) + ":"
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
