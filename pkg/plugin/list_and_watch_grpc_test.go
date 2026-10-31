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
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"

	v1 "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/rm"
)

func TestListAndWatchGRPC(t *testing.T) {
	splitCount := config.DeviceSplitCount
	config.DeviceSplitCount = 1
	t.Cleanup(func() { config.DeviceSplitCount = splitCount })
	sockets := t.TempDir()

	for _, cancelClient := range []bool{false, true} {
		name := "health update then shutdown"
		if cancelClient {
			name = "health update after client cancellation"
		}
		t.Run(name, func(t *testing.T) {
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
			}
			serverContexts := make(chan context.Context, 1)
			results := make(chan error, 1)
			server := grpc.NewServer(grpc.StreamInterceptor(func(srv interface{}, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
				serverContexts <- stream.Context()
				err := handler(srv, stream)
				results <- err
				return err
			}))
			var stop sync.Once
			t.Cleanup(func() {
				stop.Do(func() { close(plugin.stop) })
				server.Stop()
			})
			socketName := "stop.sock"
			if cancelClient {
				socketName = "cancel.sock"
			}
			socket := filepath.Join(sockets, socketName)
			listener, err := net.Listen("unix", socket)
			require.NoError(t, err)
			pluginapi.RegisterDevicePluginServer(server, plugin)
			go func() { _ = server.Serve(listener) }()
			conn, err := plugin.dial(socket, 5*time.Second)
			require.NoError(t, err)
			defer func() { require.NoError(t, conn.Close()) }()
			stream, err := pluginapi.NewDevicePluginClient(conn).ListAndWatch(ctx, &pluginapi.Empty{})
			require.NoError(t, err)
			response, err := stream.Recv()
			require.NoError(t, err)
			require.Equal(t, []*pluginapi.Device{{ID: "gpu0-0", Health: pluginapi.Healthy}}, response.Devices)
			var serverContext context.Context
			select {
			case serverContext = <-serverContexts:
			case <-ctx.Done():
				t.Fatal("server did not receive ListAndWatch")
			}

			if cancelClient {
				cancel()
				select {
				case <-serverContext.Done():
				case <-time.After(5 * time.Second):
					t.Fatal("server did not observe client cancellation")
				}
			}
			plugin.health <- device
			if !cancelClient {
				response, err = stream.Recv()
				require.NoError(t, err)
				require.Equal(t, []*pluginapi.Device{{ID: "gpu0-0", Health: pluginapi.Unhealthy}}, response.Devices)
				stop.Do(func() { close(plugin.stop) })
			}
			select {
			case err = <-results:
				if cancelClient {
					require.Error(t, err, "a real Send to the cancelled stream must not return success")
				} else {
					require.NoError(t, err)
					_, err = stream.Recv()
					require.ErrorIs(t, err, io.EOF)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("ListAndWatch did not return")
			}
		})
	}
}
