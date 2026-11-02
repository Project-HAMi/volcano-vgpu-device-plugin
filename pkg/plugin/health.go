/*
Copyright 2026 The HAMi Authors.

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
	"google.golang.org/protobuf/proto"
	"k8s.io/klog/v2"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	"volcano.sh/k8s-device-plugin/pkg/rm"
)

// watchHealth retains device health independently of ListAndWatch streams.
// Closing the notification channel wakes every watcher without waiting for a
// slow or disconnected client. Repeated unhealthy events need no new snapshot.
func (plugin *nvidiaDevicePlugin) watchHealth(stop <-chan interface{}, health <-chan *rm.Device, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		case d := <-health:
			plugin.healthMu.Lock()
			if d.Health != pluginapi.Unhealthy {
				d.Health = pluginapi.Unhealthy
				close(plugin.healthChanged)
				plugin.healthChanged = make(chan struct{})
			}
			plugin.healthMu.Unlock()
			klog.Infof("'%s' device marked unhealthy: %s", plugin.rm.Resource(), d.ID)
		}
	}
}

// deviceSnapshot copies health under the same lock as its change notification,
// so updates cannot race with serialization or be lost between sending and waiting.
func (plugin *nvidiaDevicePlugin) deviceSnapshot() ([]*pluginapi.Device, <-chan struct{}) {
	plugin.healthMu.Lock()
	defer plugin.healthMu.Unlock()
	var devices []*pluginapi.Device
	for _, d := range plugin.rm.Devices() {
		devices = append(devices, proto.Clone(&d.Device).(*pluginapi.Device))
	}
	return devices, plugin.healthChanged
}
