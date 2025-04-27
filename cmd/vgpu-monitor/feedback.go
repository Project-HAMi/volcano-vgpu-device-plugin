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

	"volcano.sh/k8s-device-plugin/pkg/monitor/nvidia"
	"volcano.sh/k8s-device-plugin/pkg/plugin/vgpu/config"

	"k8s.io/klog/v2"
)

type UtilizationPerDevice []int

func CheckBlocking(utSwitchOn map[string]UtilizationPerDevice, p int, c *nvidia.ContainerUsage) bool {
	for i := 0; i < c.Info.DeviceMax(); i++ {
		uuid := c.Info.DeviceUUID(i)
		_, ok := utSwitchOn[uuid]
		if ok {
			for i := 0; i < p; i++ {
				if utSwitchOn[uuid][i] > 0 {
					return true
				}
			}
			return false
		}
	}
	return false
}

// Check whether task with higher priority use GPU or there are other tasks with the same priority.
func CheckPriority(utSwitchOn map[string]UtilizationPerDevice, p int, c *nvidia.ContainerUsage) bool {
	for i := 0; i < c.Info.DeviceMax(); i++ {
		uuid := c.Info.DeviceUUID(i)
		_, ok := utSwitchOn[uuid]
		if ok {
			for i := 0; i < p; i++ {
				if utSwitchOn[uuid][i] > 0 {
					return true
				}
			}
			if utSwitchOn[uuid][p] > 1 {
				return true
			}
		}
	}
	return false
}

func Observe(lister *nvidia.ContainerLister) {
	utSwitchOn := map[string]UtilizationPerDevice{}
	containers := lister.ListContainers()

	for _, c := range containers {
		recentKernel := c.Info.GetRecentKernel()
		if recentKernel > 0 {
			recentKernel--
			if recentKernel > 0 {
				for i := 0; i < c.Info.DeviceMax(); i++ {
					// Null device condition
					if !c.Info.IsValidUUID(i) {
						continue
					}
					uuid := c.Info.DeviceUUID(i)
					if len(utSwitchOn[uuid]) == 0 {
						utSwitchOn[uuid] = []int{0, 0}
					}
					utSwitchOn[uuid][c.Info.GetPriority()]++
				}
			}
			c.Info.SetRecentKernel(recentKernel)
		}
	}
	for idx, c := range containers {
		priority := c.Info.GetPriority()
		recentKernel := c.Info.GetRecentKernel()
		utilizationSwitch := c.Info.GetUtilizationSwitch()
		if CheckBlocking(utSwitchOn, priority, c) {
			if recentKernel >= 0 {
				klog.Infof("utSwitchon=%v", utSwitchOn)
				klog.Infof("Setting Blocking to on %v", idx)
				c.Info.SetRecentKernel(-1)
			}
		} else {
			if recentKernel < 0 {
				klog.Infof("utSwitchon=%v", utSwitchOn)
				klog.Infof("Setting Blocking to off %v", idx)
				c.Info.SetRecentKernel(0)
			}
		}
		if CheckPriority(utSwitchOn, priority, c) {
			if utilizationSwitch != 1 {
				klog.Infof("utSwitchon=%v", utSwitchOn)
				klog.Infof("Setting UtilizationSwitch to on %v", idx)
				c.Info.SetUtilizationSwitch(1)
			}
		} else {
			if utilizationSwitch != 0 {
				klog.Infof("utSwitchon=%v", utSwitchOn)
				klog.Infof("Setting UtilizationSwitch to off %v", idx)
				c.Info.SetUtilizationSwitch(0)
			}
		}
	}
}

func watchAndFeedback(lister *nvidia.ContainerLister) {
	config.Nvml().Init()
	for {
		time.Sleep(time.Second * 5)
		err := lister.Update()
		if err != nil {
			klog.Errorf("Failed to update container list: %v", err)
			continue
		}
		Observe(lister)
	}
}
