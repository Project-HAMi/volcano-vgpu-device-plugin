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
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	v1 "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/rm"
)

// TestListAndWatchShutdownRace drives the real shutdown path (Stop -> cleanup)
// concurrently with a live ListAndWatch stream to surface the data race from
// issue #219: cleanup() writes plugin.health/plugin.stop while ListAndWatch
// reads them in its select. Run with -race; on the unpatched code the race
// detector reports a concurrent read (ListAndWatch) / write (cleanup).
func TestListAndWatchShutdownRace(t *testing.T) {
	splitCount := config.DeviceSplitCount
	config.DeviceSplitCount = 1
	t.Cleanup(func() { config.DeviceSplitCount = splitCount })

	for i := 0; i < 20; i++ {
		func() {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			device := &rm.Device{Device: pluginapi.Device{ID: "gpu0", Health: pluginapi.Healthy}}
			plugin := &nvidiaDevicePlugin{
				ctx: ctx,
				rm: &rm.ResourceManagerMock{
					DevicesFunc:  func() rm.Devices { return rm.Devices{device.ID: device} },
					ResourceFunc: func() v1.ResourceName { return "volcano.sh/vgpu-number" },
				},
				health: make(chan *rm.Device, 1),
				stop:   make(chan interface{}),
				socket: filepath.Join(t.TempDir(), "p.sock"),
			}
			server := grpc.NewServer()
			plugin.server = server
			pluginapi.RegisterDevicePluginServer(server, plugin)
			listener, err := net.Listen("unix", plugin.socket)
			require.NoError(t, err)
			go func() { _ = server.Serve(listener) }()
			conn, err := plugin.dial(plugin.socket, 5*time.Second)
			require.NoError(t, err)
			defer func() { _ = conn.Close() }()
			stream, err := pluginapi.NewDevicePluginClient(conn).ListAndWatch(ctx, &pluginapi.Empty{})
			require.NoError(t, err)
			if _, err = stream.Recv(); err != nil {
				t.Fatalf("initial ListAndWatch response: %v", err)
			}
			// Trigger the real Stop -> cleanup() shutdown while ListAndWatch is
			// blocked in its select.
			require.NoError(t, plugin.Stop())
			server.Stop()
		}()
	}
}
