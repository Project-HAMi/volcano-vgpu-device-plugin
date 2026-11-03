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
	"testing"
	"time"

	"volcano.sh/k8s-device-plugin/pkg/config"
)

func TestWatchAndRegisterStopsWithPlugin(t *testing.T) {
	mode := config.Mode
	config.Mode = ""
	t.Cleanup(func() { config.Mode = mode })

	plugin := &nvidiaDevicePlugin{}
	var stops []chan interface{}
	var dones []chan struct{}
	for range 2 {
		stop, done := make(chan interface{}), make(chan struct{})
		stops, dones = append(stops, stop), append(dones, done)
		go func() {
			plugin.WatchAndRegister(stop)
			close(done)
		}()
	}
	time.Sleep(50 * time.Millisecond)
	for _, stop := range stops {
		close(stop)
	}
	for _, done := range dones {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("WatchAndRegister kept running after its stop channel closed")
		}
	}
}

func TestSleepUnlessStopped(t *testing.T) {
	stop := make(chan interface{})
	if !sleepUnlessStopped(stop, time.Millisecond) {
		t.Fatal("expected the timer to fire while stop is open")
	}
	close(stop)
	if sleepUnlessStopped(stop, time.Minute) {
		t.Fatal("expected a closed stop channel to end the wait")
	}
	for range 200 {
		if sleepUnlessStopped(stop, 0) {
			t.Fatal("a closed stop channel must win over an expired timer")
		}
	}
}
