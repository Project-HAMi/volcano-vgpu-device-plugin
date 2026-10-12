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
	"fmt"
	"os"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"volcano.sh/k8s-device-plugin/pkg/vgpucache"
)

const (
	defaultVGPUCacheRoot         = "/usr/local/vgpu/vgpu/containers"
	defaultVGPUCacheScanInterval = 5 * time.Second
	defaultVGPUCacheGracePeriod  = 5 * time.Minute
	vgpuCacheRootEnvName         = "HAMI_VGPU_CACHE_ROOT"
	vgpuCacheGracePeriodEnvName  = "HAMI_VGPU_CACHE_GRACE_PERIOD"
)

// resolveVGPUCacheConfig resolves the shared cache root and GC grace period from the deployment
// environment.
func resolveVGPUCacheConfig() vgpucache.Config {
	root := os.Getenv(vgpuCacheRootEnvName)
	if root == "" {
		hookPath := os.Getenv("HOOK_PATH")
		if hookPath == "" {
			root = defaultVGPUCacheRoot
		} else {
			root = filepath.Join(hookPath, "vgpu", "containers")
		}
	}

	gracePeriod := defaultVGPUCacheGracePeriod
	if raw := os.Getenv(vgpuCacheGracePeriodEnvName); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			klog.Warningf("Invalid %s value %q, using default %v", vgpuCacheGracePeriodEnvName, raw, gracePeriod)
		} else {
			gracePeriod = parsed
		}
	}
	return vgpucache.Config{
		Root:         root,
		ScanInterval: defaultVGPUCacheScanInterval,
		GracePeriod:  gracePeriod,
	}
}

// startVGPUCacheEvents queues one warning per abandoned directory. A bounded
// worker issues no LISTs, limits reports to one per five seconds, and does not
// retry failed notifications: the cleanup decision and Error log remain valid.
func startVGPUCacheEvents(ctx context.Context, kubeClient kubernetes.Interface, nodeName string) func(string, int, error) {
	messages := make(chan string, 16)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case message := <-messages:
				if ctx.Err() != nil {
					return
				}
				reportCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				node, err := kubeClient.CoreV1().Nodes().Get(reportCtx, nodeName, metav1.GetOptions{})
				if err == nil {
					now := metav1.Now()
					_, err = kubeClient.CoreV1().Events(corev1.NamespaceDefault).Create(reportCtx, &corev1.Event{
						ObjectMeta:     metav1.ObjectMeta{GenerateName: "vgpu-cache-cleanup-", Namespace: corev1.NamespaceDefault},
						InvolvedObject: corev1.ObjectReference{APIVersion: "v1", Kind: "Node", Name: node.Name, UID: node.UID},
						Type:           corev1.EventTypeWarning, Reason: "VGPUCacheCleanupAbandoned", Message: message,
						Source:         corev1.EventSource{Component: "volcano-vgpu-device-plugin", Host: nodeName},
						FirstTimestamp: now, LastTimestamp: now, Count: 1,
					}, metav1.CreateOptions{})
				}
				cancel()
				if err != nil {
					klog.ErrorS(err, "Failed to report abandoned vGPU cache cleanup", "node", nodeName, "message", message)
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
				}
			}
		}
	}()
	return func(path string, attempts int, err error) {
		message := fmt.Sprintf("Stopped cleaning %s after %d deletion failures: %v. Remove it manually or restart the device plugin after fixing the cause.", path, attempts, err)
		select {
		case <-ctx.Done():
		case messages <- message:
		default:
			klog.ErrorS(nil, "vGPU cache warning queue is full; notification dropped", "message", message)
		}
	}
}
