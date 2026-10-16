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
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"volcano.sh/k8s-device-plugin/pkg/monitor/nvidia"
	"volcano.sh/k8s-device-plugin/pkg/nodepodinformer"

	"k8s.io/klog/v2"
)

func init() {
	// Do not register to flag.CommandLine to avoid conflict with urfave/cli's -v (version) flag
	klogFlags := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(klogFlags)

	// Set klog log level, configurable via KLOG_LEVEL environment variable, default is 2
	level := os.Getenv("KLOG_LEVEL")
	if level == "" {
		level = "2"
	}
	_ = klogFlags.Set("v", level)
}

func main() {
	if err := ValidateEnvVars(); err != nil {
		klog.Fatalf("Failed to validate environment variables: %v", err)
	}
	containerLister, err := nvidia.NewContainerLister()
	if err != nil {
		klog.Fatalf("Failed to create container lister: %v", err)
	}
	defer containerLister.Close()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := wireNodePodInformer(ctx, containerLister); err != nil {
		klog.Fatalf("Failed to start node Pod informer: %v", err)
	}

	errchannel := make(chan error)
	go initMetrics(containerLister)
	go watchAndFeedback(containerLister)
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errchannel:
			klog.Errorf("failed to serve: %v", err)
		}
	}
}

// wireNodePodInformer attaches a node-scoped Pod informer so Update/metrics read
// the local cache instead of Listing Pods from the API server every feedback tick.
// When NODE_NAME is unset, ListNodePods keeps its API List fallback.
func wireNodePodInformer(ctx context.Context, containerLister *nvidia.ContainerLister) error {
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		klog.Warning("NODE_NAME unset; monitor will List Pods from the API server on each Update")
		return nil
	}
	nodePods, err := nodepodinformer.New(containerLister.Clientset(), nodeName)
	if err != nil {
		return err
	}
	nodePods.Start(ctx)
	syncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := nodePods.WaitForSync(syncCtx); err != nil {
		return err
	}
	containerLister.UseNodePodList(nodePods.List)
	klog.InfoS("Monitor node Pod informer synced", "node", nodeName)
	return nil
}
