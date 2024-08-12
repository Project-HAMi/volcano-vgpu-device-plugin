/*
Copyright 2024 The HAMi Authors.

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

package main

import (
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"k8s.io/klog/v2"
)

type UtilizationPerDevice []int

var srPodList map[string]podusage

func init() {
	srPodList = make(map[string]podusage)
}

func CheckBlocking(utSwitchOn map[string]UtilizationPerDevice, p int, pu podusage) bool {
	for _, devuuid := range pu.sr.uuids {
		_, ok := utSwitchOn[string(devuuid.uuid[:])]
		if ok {
			for i := 0; i < p; i++ {
				if utSwitchOn[string(devuuid.uuid[:])][i] > 0 {
					return true
				}
			}
			return false
		}
	}
	return false
}

// Check whether task with higher priority use GPU or there are other tasks with the same priority.
func CheckPriority(utSwitchOn map[string]UtilizationPerDevice, p int, pu podusage) bool {
	for _, devuuid := range pu.sr.uuids {
		_, ok := utSwitchOn[string(devuuid.uuid[:])]
		if ok {
			for i := 0; i < p; i++ {
				if utSwitchOn[string(devuuid.uuid[:])][i] > 0 {
					return true
				}
			}
			if utSwitchOn[string(devuuid.uuid[:])][p] > 1 {
				return true
			}
		}
	}
	return false
}

func Observe(srlist *map[string]podusage) error {
	utSwitchOn := map[string]UtilizationPerDevice{}

	for idx, val := range *srlist {
		if val.sr == nil {
			continue
		}
		if val.sr.recentKernel > 0 {
			(*srlist)[idx].sr.recentKernel--
			if (*srlist)[idx].sr.recentKernel > 0 {
				for _, devuuid := range val.sr.uuids {
					// Null device condition
					if devuuid.uuid[0] == 0 {
						continue
					}
					if len(utSwitchOn[string(devuuid.uuid[:])]) == 0 {
						utSwitchOn[string(devuuid.uuid[:])] = []int{0, 0}
					}
					utSwitchOn[string(devuuid.uuid[:])][val.sr.priority]++
				}
			}
		}
	}
	for idx, val := range *srlist {
		if val.sr == nil {
			continue
		}
		if CheckBlocking(utSwitchOn, int(val.sr.priority), val) {
			if (*srlist)[idx].sr.recentKernel >= 0 {
				klog.Infof("utSwitchon=%v", utSwitchOn)
				klog.Infof("Setting Blocking to on %v", idx)
				(*srlist)[idx].sr.recentKernel = -1
			}
		} else {
			if (*srlist)[idx].sr.recentKernel < 0 {
				klog.Infof("utSwitchon=%v", utSwitchOn)
				klog.Infof("Setting Blocking to off %v", idx)
				(*srlist)[idx].sr.recentKernel = 0
			}
		}
		if CheckPriority(utSwitchOn, int(val.sr.priority), val) {
			if (*srlist)[idx].sr.utilizationSwitch != 1 {
				klog.Infof("utSwitchon=%v", utSwitchOn)
				klog.Infof("Setting UtilizationSwitch to on %v", idx)
				(*srlist)[idx].sr.utilizationSwitch = 1
			}
		} else {
			if (*srlist)[idx].sr.utilizationSwitch != 0 {
				klog.Infof("utSwitchon=%v", utSwitchOn)
				klog.Infof("Setting UtilizationSwitch to off %v", idx)
				(*srlist)[idx].sr.utilizationSwitch = 0
			}
		}
	}
	return nil
}

func watchAndFeedback() {
	nvml.Init()
	for {
		time.Sleep(time.Second * 5)
		err := monitorPath(srPodList)
		if err != nil {
			klog.Errorf("monitorPath failed %v", err.Error())
		}
		klog.Infof("WatchAndFeedback srPodList=%v", srPodList)
		Observe(&srPodList)
	}
}
