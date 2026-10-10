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

package nvidia

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	v0 "volcano.sh/k8s-device-plugin/pkg/monitor/nvidia/v0"
	v1 "volcano.sh/k8s-device-plugin/pkg/monitor/nvidia/v1"
	"volcano.sh/k8s-device-plugin/pkg/vgpucache"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

const SharedRegionMagicFlag = 19920718

type headerT struct {
	initializedFlag int32
	majorVersion    int32
	minorVersion    int32
}

type UsageInfo interface {
	DeviceMax() int
	DeviceNum() int
	DeviceMemoryContextSize(idx int) uint64
	DeviceMemoryModuleSize(idx int) uint64
	DeviceMemoryBufferSize(idx int) uint64
	DeviceMemoryOffset(idx int) uint64
	DeviceMemoryTotal(idx int) uint64
	DeviceSmUtil(idx int) uint64
	IsValidUUID(idx int) bool
	DeviceUUID(idx int) string
	DeviceMemoryLimit(idx int) uint64
	LastKernelTime() int64
	//UsedMemory(idx int) (uint64, error)
	GetPriority() int
	GetRecentKernel() int32
	SetRecentKernel(v int32)
	GetUtilizationSwitch() int32
	SetUtilizationSwitch(v int32)
}

type ContainerUsage struct {
	PodUID        string
	ContainerName string
	data          []byte
	Info          UsageInfo
	directoryInfo os.FileInfo
}

type ContainerLister struct {
	containerPath string
	containers    map[string]*ContainerUsage
	mutex         sync.Mutex
	clientset     kubernetes.Interface
}

func NewContainerLister() (*ContainerLister, error) {
	hookPath, ok := os.LookupEnv("HOOK_PATH")
	if !ok {
		return nil, fmt.Errorf("HOOK_PATH not set")
	}
	config, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	if err != nil {
		klog.Errorf("Failed to build kubeconfig: %v", err)
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Errorf("Failed to build clientset: %v", err)
		return nil, err
	}
	return &ContainerLister{
		containerPath: filepath.Join(hookPath, "/vgpu/containers"),
		containers:    make(map[string]*ContainerUsage),
		clientset:     clientset,
	}, nil
}

// WithContainers keeps mappings alive for the duration of fn. Neither the map nor
// its usage pointers may escape fn. Feedback writes and metrics reads are serialized
// with unmapping to prevent use-after-unmap on concurrent scrapes or cache replacement.
func (l *ContainerLister) WithContainers(fn func(map[string]*ContainerUsage)) {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	fn(l.containers)
}

func (l *ContainerLister) Clientset() kubernetes.Interface {
	return l.clientset
}

func (l *ContainerLister) Update() error {
	// Scope pod listing to this node to avoid cluster-wide API calls on every
	// scrape. NODE_NAME is injected via the Downward API in the official manifest.
	// Fall back to listing all pods if unset to preserve backward compatibility
	// with custom deployments that may not have NODE_NAME configured.
	listOptions := metav1.ListOptions{}
	if nodeName := os.Getenv("NODE_NAME"); nodeName != "" {
		listOptions.FieldSelector = "spec.nodeName=" + nodeName
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pods, err := l.clientset.CoreV1().Pods("").List(ctx, listOptions)
	if err != nil {
		return err
	}

	return l.reconcile(pods)
}

func (l *ContainerLister) reconcile(pods *corev1.PodList) error {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	entries, err := os.ReadDir(l.containerPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Only the device plugin owns directory deletion. The monitor only manages
	// mappings, including directories removed/replaced between updates.
	present := make(map[string]os.FileInfo)
	for _, entry := range entries {
		if !entry.IsDir() || !isValidPod(entry.Name(), pods) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		present[entry.Name()] = info
	}
	for name, usage := range l.containers {
		info, exists := present[name]
		if exists && usage.directoryInfo != nil && os.SameFile(info, usage.directoryInfo) {
			continue
		}
		if err := syscall.Munmap(usage.data); err != nil {
			return fmt.Errorf("unmap cache %s: %w", name, err)
		}
		delete(l.containers, name)
	}
	for name, info := range present {
		if _, exists := l.containers[name]; exists {
			continue
		}
		dirName := filepath.Join(l.containerPath, name)
		usage, err := loadCache(dirName)
		if err != nil {
			klog.Errorf("Failed to load cache %s: %v", dirName, err)
			continue
		}
		if usage == nil {
			continue
		}
		// Preparation may replace the directory during mmap. Never publish a mapping
		// whose identity no longer matches the snapshot; retry on the next update.
		current, err := os.Lstat(dirName)
		if err != nil || !current.IsDir() || !os.SameFile(info, current) {
			if unmapErr := syscall.Munmap(usage.data); unmapErr != nil {
				return unmapErr
			}
			continue
		}
		usage.PodUID, usage.ContainerName, _ = vgpucache.ParseDirectoryName(name)
		usage.directoryInfo = info
		l.containers[name] = usage
	}
	return nil
}

func loadCache(fpath string) (*ContainerUsage, error) {
	klog.Infof("Checking path %s", fpath)
	files, err := os.ReadDir(fpath)
	if err != nil {
		return nil, err
	}
	if len(files) > 2 {
		return nil, errors.New("cache num not matched")
	}
	if len(files) == 0 {
		return nil, nil
	}
	cacheFile := ""
	for _, val := range files {
		if strings.Contains(val.Name(), "libvgpu.so") {
			continue
		}
		if !strings.Contains(val.Name(), ".cache") {
			continue
		}
		cacheFile = filepath.Join(fpath, val.Name())
		break
	}
	if cacheFile == "" {
		klog.Infof("No cache file in %s", fpath)
		return nil, nil
	}
	info, err := os.Stat(cacheFile)
	if err != nil {
		klog.Errorf("Failed to stat cache file: %s, error: %v", cacheFile, err)
		return nil, err
	}
	if info.Size() < int64(unsafe.Sizeof(headerT{})) {
		return nil, fmt.Errorf("cache file size %d too small", info.Size())
	}
	f, err := os.OpenFile(cacheFile, os.O_RDWR, 0666)
	if err != nil {
		klog.Errorf("Failed to open cache file: %s, error: %v", cacheFile, err)
		return nil, err
	}
	defer func(f *os.File) {
		_ = f.Close()
	}(f)
	usage := &ContainerUsage{}
	usage.data, err = syscall.Mmap(int(f.Fd()), 0, int(info.Size()), syscall.PROT_WRITE|syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		klog.Errorf("Failed to mmap cache file: %s, error: %v", cacheFile, err)
		return nil, err
	}
	head := (*headerT)(unsafe.Pointer(&usage.data[0]))
	if head.initializedFlag != SharedRegionMagicFlag {
		_ = syscall.Munmap(usage.data)
		return nil, fmt.Errorf("cache file magic flag not matched")
	}
	if info.Size() == 1197897 {
		usage.Info = v0.CastSpec(usage.data)
	} else if head.majorVersion == 1 {
		usage.Info = v1.CastSpec(usage.data)
	} else {
		_ = syscall.Munmap(usage.data)
		return nil, fmt.Errorf("unknown cache file size %d version %d.%d", info.Size(), head.majorVersion, head.minorVersion)
	}
	return usage, nil
}

func isValidPod(name string, pods *corev1.PodList) bool {
	uid, container, ok := vgpucache.ParseDirectoryName(name)
	if !ok || pods == nil {
		return false
	}
	for _, pod := range pods.Items {
		if string(pod.UID) != uid {
			continue
		}
		for _, c := range pod.Spec.Containers {
			if c.Name == container {
				return true
			}
		}
		for _, c := range pod.Spec.InitContainers {
			if c.Name == container {
				return true
			}
		}
	}
	return false
}
