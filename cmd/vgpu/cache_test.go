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

package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	"os"
	"volcano.sh/k8s-device-plugin/pkg/vgpucache"

	"testing"
	"time"
)

func TestResolveVGPUCacheConfigReusesExistingEnvironment(t *testing.T) {
	t.Setenv(vgpuCacheRootEnvName, "/var/lib/hami/containers")
	t.Setenv(vgpuCacheGracePeriodEnvName, "7m")
	config := resolveVGPUCacheConfig()
	if config.Root != "/var/lib/hami/containers" {
		t.Fatalf("Root = %q", config.Root)
	}
	if config.GracePeriod != 7*time.Minute {
		t.Fatalf("GracePeriod = %v", config.GracePeriod)
	}
	if config.ScanInterval != defaultVGPUCacheScanInterval {
		t.Fatalf("ScanInterval = %v", config.ScanInterval)
	}
}

func TestResolveVGPUCacheConfigDefaultsFromHookPath(t *testing.T) {
	t.Setenv(vgpuCacheRootEnvName, "")
	t.Setenv(vgpuCacheGracePeriodEnvName, "invalid")
	t.Setenv("HOOK_PATH", "/opt/hami")
	config := resolveVGPUCacheConfig()
	if config.Root != "/opt/hami/vgpu/containers" {
		t.Fatalf("Root = %q", config.Root)
	}
	if config.GracePeriod != defaultVGPUCacheGracePeriod {
		t.Fatalf("GracePeriod = %v", config.GracePeriod)
	}
}

func TestVGPUCacheAbandonEvent(t *testing.T) {
	for _, failWrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("failWrite=%v", failWrite), func(t *testing.T) {
			kubeClient := fake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu-node", UID: "node-uid"}})
			created := make(chan *corev1.Event, 1)
			kubeClient.PrependReactor("create", "events", func(action clienttesting.Action) (bool, runtime.Object, error) {
				create, ok := action.(clienttesting.CreateAction)
				if !ok {
					return true, nil, errors.New("unexpected action")
				}
				event, ok := create.GetObject().(*corev1.Event)
				if !ok {
					return true, nil, errors.New("unexpected object")
				}
				created <- event.DeepCopy()
				if failWrite {
					return true, nil, errors.New("event API unavailable")
				}
				return true, event, nil
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			report := startVGPUCacheEvents(ctx, kubeClient, "gpu-node")
			report("/cache/pod_gpu", 5, errors.New("permission denied"))
			select {
			case event := <-created:
				require.Equal(t, "VGPUCacheCleanupAbandoned", event.Reason)
				require.Equal(t, corev1.EventTypeWarning, event.Type)
				require.Equal(t, "Node", event.InvolvedObject.Kind)
				require.Equal(t, "node-uid", string(event.InvolvedObject.UID))
				require.Equal(t, "gpu-node", event.InvolvedObject.Name)
				require.Contains(t, event.Message, "/cache/pod_gpu")
				require.Contains(t, event.Message, "5 deletion failures")
				require.Contains(t, event.Message, "permission denied")
			case <-time.After(time.Second):
				t.Fatal("event was not reported")
			}
			cancel()
			actions := kubeClient.Actions()
			require.Len(t, actions, 2)
			require.Equal(t, "get", actions[0].GetVerb())
			require.Equal(t, "create", actions[1].GetVerb())
		})
	}
}

func TestVGPUCacheEventQueueDoesNotBlock(t *testing.T) {
	kubeClient := fake.NewSimpleClientset()
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	kubeClient.PrependReactor("get", "nodes", func(clienttesting.Action) (bool, runtime.Object, error) {
		close(entered)
		<-release
		return true, nil, errors.New("Node API unavailable")
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	report := startVGPUCacheEvents(ctx, kubeClient, "gpu-node")
	report("/cache/first_gpu", 5, os.ErrPermission)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	queued := make(chan struct{})
	go func() {
		for range 32 {
			report("/cache/other_gpu", 5, os.ErrPermission)
		}
		close(queued)
	}()
	select {
	case <-queued:
	case <-time.After(time.Second):
		t.Fatal("full notification queue blocked cleanup")
	}
}

func TestVGPUCacheGracePeriodIndependentOfMonitor(t *testing.T) {
	for _, tc := range []struct {
		name, grace, resync string
		want                time.Duration
		invalid             bool
	}{
		{name: "default", want: 5 * time.Minute},
		{name: "legacy setting ignored", resync: "1s", want: 5 * time.Minute},
		{name: "independent custom values", grace: "7m", resync: "1s", want: 7 * time.Minute},
		{name: "invalid uses default", grace: "invalid", resync: "1s", want: 5 * time.Minute},
		{name: "zero grace", grace: "0s", resync: "5m"},
		{name: "negative rejected", grace: "-1s", want: -time.Second, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HAMI_VGPU_CACHE_GRACE_PERIOD", tc.grace)
			t.Setenv("HAMI_RESYNC_INTERVAL", tc.resync)
			t.Setenv(vgpuCacheRootEnvName, t.TempDir())
			cfg := resolveVGPUCacheConfig()
			require.Equal(t, tc.want, cfg.GracePeriod)
			list := func() ([]*corev1.Pod, error) { return nil, nil }
			_, err := vgpucache.New(cfg, list, list)
			if tc.invalid {
				require.ErrorContains(t, err, "grace period must not be negative")
			} else {
				require.NoError(t, err)
			}
		})
	}
}
