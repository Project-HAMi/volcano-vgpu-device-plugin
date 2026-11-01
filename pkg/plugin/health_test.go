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
	"errors"
	"fmt"
	"io"
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

type healthTestStream struct {
	grpc.ServerStream
	ctx  context.Context
	send func(*pluginapi.ListAndWatchResponse) error
}

func (s *healthTestStream) Context() context.Context { return s.ctx }
func (s *healthTestStream) Send(response *pluginapi.ListAndWatchResponse) error {
	return s.send(response)
}

type healthTestEvent struct {
	device    *rm.Device
	delivered chan struct{}
}

// notifyHealth waits until CheckHealth's notification has been consumed.
func notifyHealth(t *testing.T, events chan<- healthTestEvent, device *rm.Device) {
	t.Helper()
	event := healthTestEvent{device: device, delivered: make(chan struct{})}
	select {
	case events <- event:
	case <-time.After(5 * time.Second):
		t.Fatal("health producer did not accept an event")
	}
	select {
	case <-event.delivered:
	case <-time.After(5 * time.Second):
		t.Fatal("health notification blocked while the plugin was running")
	}
}

// healthTestPlugin starts the production notification consumer and a controllable
// health producer without initializing GPU hardware or changing global NVML state.
func healthTestPlugin(t *testing.T) (*nvidiaDevicePlugin, chan<- healthTestEvent, []*rm.Device) {
	t.Helper()
	split := config.DeviceSplitCount
	config.DeviceSplitCount = 1
	t.Cleanup(func() { config.DeviceSplitCount = split })
	devices := []*rm.Device{
		{Device: pluginapi.Device{ID: "gpu0", Health: pluginapi.Healthy}},
		{Device: pluginapi.Device{ID: "gpu1", Health: pluginapi.Healthy}},
	}
	events := make(chan healthTestEvent)
	producerDone := make(chan struct{})
	manager := &rm.ResourceManagerMock{
		DevicesFunc:  func() rm.Devices { return rm.Devices{devices[0].ID: devices[0], devices[1].ID: devices[1]} },
		ResourceFunc: func() v1.ResourceName { return "volcano.sh/vgpu-number" },
		CheckHealthFunc: func(stop <-chan interface{}, unhealthy chan<- *rm.Device) error {
			defer close(producerDone)
			for {
				select {
				case <-stop:
					return nil
				case event := <-events:
					select {
					case unhealthy <- event.device:
						close(event.delivered)
					case <-stop:
						return nil
					}
				}
			}
		},
	}
	plugin := &nvidiaDevicePlugin{rm: manager, ctx: t.Context()}
	plugin.initialize()
	go func(stop <-chan interface{}, health chan<- *rm.Device) { _ = manager.CheckHealth(stop, health) }(plugin.stop, plugin.health)
	t.Cleanup(func() {
		require.NoError(t, plugin.Stop())
		select {
		case <-producerDone:
		case <-time.After(5 * time.Second):
			t.Error("health producer did not stop")
		}
	})
	return plugin, events, devices
}

// TestHealthAfterStreamFailure verifies that health events keep making progress
// with no stream, and a reconnect observes every device marked unhealthy.
func TestHealthAfterStreamFailure(t *testing.T) {
	for _, failOn := range []int{1, 2} {
		t.Run(fmt.Sprintf("send-%d", failOn), func(t *testing.T) {
			plugin, events, devices := healthTestPlugin(t)
			failed := errors.New("stream send failed")
			sent := make(chan struct{}, 2)
			done := make(chan error, 1)
			go func() {
				sends := 0
				done <- plugin.ListAndWatch(&pluginapi.Empty{}, &healthTestStream{ctx: t.Context(), send: func(*pluginapi.ListAndWatchResponse) error {
					sends++
					sent <- struct{}{}
					if sends == failOn {
						return failed
					}
					return nil
				}})
			}()
			select {
			case <-sent:
			case <-time.After(5 * time.Second):
				t.Fatal("initial Send did not run")
			}
			if failOn == 2 {
				notifyHealth(t, events, devices[0])
			}
			select {
			case err := <-done:
				require.ErrorIs(t, err, failed)
			case <-time.After(5 * time.Second):
				t.Fatal("failed stream did not exit")
			}
			for _, device := range []*rm.Device{devices[0], devices[1], devices[0], devices[1]} {
				notifyHealth(t, events, device)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			stream := &healthTestStream{ctx: ctx, send: func(response *pluginapi.ListAndWatchResponse) error {
				require.Len(t, response.Devices, 2)
				for _, device := range response.Devices {
					require.Equal(t, pluginapi.Unhealthy, device.Health)
				}
				cancel()
				return nil
			}}
			require.ErrorIs(t, plugin.ListAndWatch(&pluginapi.Empty{}, stream), context.Canceled)
		})
	}
}

// TestHealthDuringSlowSend ensures a blocked stream cannot stall health intake,
// and each active stream receives the retained state without racing serialization.
func TestHealthDuringSlowSend(t *testing.T) {
	plugin, events, devices := healthTestPlugin(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sending := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	done := make(chan error, 1)
	go func() {
		first := true
		done <- plugin.ListAndWatch(&pluginapi.Empty{}, &healthTestStream{ctx: ctx, send: func(response *pluginapi.ListAndWatchResponse) error {
			if first {
				first = false
				close(sending)
				<-release
				// The initial response must remain an immutable healthy snapshot.
				for _, d := range response.Devices {
					if d.Health != pluginapi.Healthy {
						return errors.New("initial snapshot was mutated")
					}
				}
				return nil
			}
			for _, d := range response.Devices {
				if d.Health != pluginapi.Unhealthy {
					return errors.New("health update was lost")
				}
			}
			cancel()
			return nil
		}})
	}()
	select {
	case <-sending:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not start")
	}
	for _, d := range []*rm.Device{devices[0], devices[1], devices[0], devices[1]} {
		notifyHealth(t, events, d)
	}
	// A second stream must see current health even while the first Send is blocked.
	secondCtx, secondCancel := context.WithCancel(t.Context())
	defer secondCancel()
	require.ErrorIs(t, plugin.ListAndWatch(&pluginapi.Empty{}, &healthTestStream{ctx: secondCtx, send: func(response *pluginapi.ListAndWatchResponse) error {
		for _, d := range response.Devices {
			require.Equal(t, pluginapi.Unhealthy, d.Health)
		}
		secondCancel()
		return nil
	}}), context.Canceled)
	select {
	case release <- struct{}{}:
	case <-time.After(5 * time.Second):
		t.Fatal("could not release slow Send")
	}
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("first stream did not finish")
	}
}

// TestHealthStreamReconnect uses real Unix-socket gRPC to cover client
// cancellation, healthy updates, reconnect snapshots, and clean server shutdown.
func TestHealthStreamReconnect(t *testing.T) {
	plugin, events, devices := healthTestPlugin(t)
	socket := filepath.Join(t.TempDir(), "health.sock")
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	results := make(chan error, 4)
	server := grpc.NewServer(grpc.StreamInterceptor(func(srv interface{}, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		err := handler(srv, stream)
		results <- err
		return err
	}))
	t.Cleanup(server.Stop)
	pluginapi.RegisterDevicePluginServer(server, plugin)
	go func() { _ = server.Serve(listener) }()
	conn, err := plugin.dial(socket, 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := pluginapi.NewDevicePluginClient(conn)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := client.ListAndWatch(ctx, &pluginapi.Empty{})
	require.NoError(t, err)
	initial, err := stream.Recv()
	require.NoError(t, err)
	require.Len(t, initial.Devices, 2)
	for _, d := range initial.Devices {
		require.Equal(t, pluginapi.Healthy, d.Health)
	}
	cancel()
	select {
	case err = <-results:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled stream did not exit")
	}
	for _, d := range []*rm.Device{devices[0], devices[0], devices[0]} {
		notifyHealth(t, events, d)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err = client.ListAndWatch(ctx, &pluginapi.Empty{})
	require.NoError(t, err)
	response, err := stream.Recv()
	require.NoError(t, err)
	for _, d := range response.Devices {
		if d.ID == "gpu0-0" {
			require.Equal(t, pluginapi.Unhealthy, d.Health)
		} else {
			require.Equal(t, pluginapi.Healthy, d.Health)
		}
	}
	second, err := client.ListAndWatch(ctx, &pluginapi.Empty{})
	require.NoError(t, err)
	_, err = second.Recv()
	require.NoError(t, err)
	notifyHealth(t, events, devices[1])
	response, err = stream.Recv()
	require.NoError(t, err)
	for _, d := range response.Devices {
		require.Equal(t, pluginapi.Unhealthy, d.Health)
	}
	response, err = second.Recv()
	require.NoError(t, err)
	for _, d := range response.Devices {
		require.Equal(t, pluginapi.Unhealthy, d.Health)
	}
	require.NoError(t, plugin.Stop())
	select {
	case err = <-results:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not stop")
	}
	_, err = stream.Recv()
	require.ErrorIs(t, err, io.EOF)
}
