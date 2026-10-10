package plugin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	corev1 "k8s.io/api/core/v1"
	spec "volcano.sh/k8s-device-plugin/api/config/v1"
	"volcano.sh/k8s-device-plugin/pkg/config"
	"volcano.sh/k8s-device-plugin/pkg/nodepodinformer"
	"volcano.sh/k8s-device-plugin/pkg/util"
	"volcano.sh/k8s-device-plugin/pkg/util/client"
	"volcano.sh/k8s-device-plugin/pkg/vgpucache"
)

// The main GPU resource is the only owner of cache preparation and GC. Stop waits
// for its worker before another plugin instance starts against the same root.
func (plugin *nvidiaDevicePlugin) startCacheManager() error {
	if plugin.rm.Resource() != spec.ResourceName(util.ResourceName) || config.Mode == "mig" {
		return nil
	}
	hookPath := os.Getenv("HOOK_PATH")
	if !filepath.IsAbs(hookPath) {
		return fmt.Errorf("HOOK_PATH must be absolute: %q", hookPath)
	}
	kube, err := client.NewClient()
	if err != nil {
		return fmt.Errorf("create cache Pod client: %w", err)
	}
	pods, err := nodepodinformer.New(kube, os.Getenv("NODE_NAME"))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(plugin.ctx)
	manager, err := vgpucache.New(vgpucache.Config{
		Root:         filepath.Join(hookPath, "vgpu", "containers"),
		ScanInterval: 5 * time.Second,
		GracePeriod:  5 * time.Minute,
	}, pods.List, func() ([]*corev1.Pod, error) { return pods.ListFresh(ctx) })
	if err != nil {
		cancel()
		return err
	}
	plugin.cacheManager, plugin.cacheCancel, plugin.cacheDone = manager, cancel, make(chan struct{})
	pods.Start(ctx)
	go func() { defer close(plugin.cacheDone); manager.Run(ctx) }()
	return nil
}

func (plugin *nvidiaDevicePlugin) stopCacheManager() {
	if plugin.cacheCancel == nil {
		return
	}
	plugin.cacheCancel()
	<-plugin.cacheDone
	plugin.cacheCancel, plugin.cacheDone, plugin.cacheManager = nil, nil, nil
}

func (plugin *nvidiaDevicePlugin) prepareCache(podUID, containerName string) (string, error) {
	if plugin.cacheManager == nil {
		return "", fmt.Errorf("vGPU cache manager is not initialized")
	}
	// This shared lock mount is also required for successful libvgpu startup.
	if err := os.MkdirAll("/tmp/vgpulock", 0777); err != nil {
		return "", fmt.Errorf("create vGPU lock directory: %w", err)
	}
	if err := os.Chmod("/tmp/vgpulock", 0777); err != nil {
		return "", fmt.Errorf("set vGPU lock directory permissions: %w", err)
	}
	return plugin.cacheManager.Prepare(podUID, containerName)
}
