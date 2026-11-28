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

package client

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

const (
	// Use the client-go REST client defaults for unset or invalid settings.
	defaultKubeAPIQPS   = rest.DefaultQPS
	defaultKubeAPIBurst = rest.DefaultBurst
)

var (
	kubeClient kubernetes.Interface
	once       sync.Once
)

// GetClient returns the shared Kubernetes client singleton.
func GetClient() kubernetes.Interface {
	once.Do(func() {
		var err error
		kubeClient, err = NewClient()
		if err != nil {
			klog.Errorf("Failed to initialize Kubernetes client: %v", err)
			panic(err)
		}
	})
	return kubeClient
}

// SetClient overrides the Kubernetes client singleton (primarily for testing).
func SetClient(c kubernetes.Interface) {
	kubeClient = c
	once = sync.Once{}
	once.Do(func() {})
}

// ResetClient resets the Kubernetes client singleton (primarily for testing).
func ResetClient() {
	kubeClient = nil
	once = sync.Once{}
}

// NewClient connects to an API server.
func NewClient() (kubernetes.Interface, error) {
	kubeConfig := os.Getenv("KUBECONFIG")
	if kubeConfig == "" {
		kubeConfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
	}
	config, err := clientcmd.BuildConfigFromFlags("", kubeConfig)
	if err != nil {
		klog.Infof("BuildConfigFromFlags failed for file %s: %v using inClusterConfig", kubeConfig, err)
		config, err = rest.InClusterConfig()
		if err != nil {
			klog.Errorf("InClusterConfig Failed for err:%s", err.Error())
			return nil, err
		}
	}
	configureRateLimits(config, os.Getenv)

	KubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Errorf("new config error %s", err.Error())
	}
	return KubeClient, err
}

func configureRateLimits(config *rest.Config, getenv func(string) string) {
	config.QPS, config.Burst = kubeAPIRateLimits(getenv)
	config.RateLimiter = nil
	klog.Infof("Kubernetes client rate limits: QPS=%v, Burst=%d", config.QPS, config.Burst)
}

// kubeAPIRateLimits reads the Kubernetes API client rate limits.
func kubeAPIRateLimits(getenv func(string) string) (qps float32, burst int) {
	return parseKubeAPIQPS(getenv("KUBE_API_QPS")), parseKubeAPIBurst(getenv("KUBE_API_BURST"))
}

// parseKubeAPIQPS returns the default for invalid or unset values.
func parseKubeAPIQPS(raw string) float32 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultKubeAPIQPS
	}
	qps, err := strconv.ParseFloat(raw, 32)
	if err != nil {
		klog.Warningf("Invalid KUBE_API_QPS %q: %v; falling back to default %v", raw, err, defaultKubeAPIQPS)
		return defaultKubeAPIQPS
	}
	if math.IsNaN(qps) || math.IsInf(qps, 0) || qps <= 0 {
		klog.Warningf("Invalid KUBE_API_QPS %q: must be a positive finite number; falling back to default %v", raw, defaultKubeAPIQPS)
		return defaultKubeAPIQPS
	}
	return float32(qps)
}

// parseKubeAPIBurst returns the default for invalid or unset values.
func parseKubeAPIBurst(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultKubeAPIBurst
	}
	burst, err := strconv.Atoi(raw)
	if err != nil {
		klog.Warningf("Invalid KUBE_API_BURST %q: %v; falling back to default %d", raw, err, defaultKubeAPIBurst)
		return defaultKubeAPIBurst
	}
	if burst <= 0 {
		klog.Warningf("Invalid KUBE_API_BURST %q: must be positive; falling back to default %d", raw, defaultKubeAPIBurst)
		return defaultKubeAPIBurst
	}
	return burst
}
