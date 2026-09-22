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
	"flag"
	"os"

	"volcano.sh/k8s-device-plugin/pkg/monitor/nvidia"

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
	errchannel := make(chan error)
	go initMetrics(containerLister)
	go watchAndFeedback(containerLister)
	for {
		err := <-errchannel
		klog.Errorf("failed to serve: %v", err)
	}
}
