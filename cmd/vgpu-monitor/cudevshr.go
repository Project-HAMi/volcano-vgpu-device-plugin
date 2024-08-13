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
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/exp/mmap"
)

type deviceMemory struct {
	contextSize uint64
	moduleSize  uint64
	bufferSize  uint64
	offset      uint64
	total       uint64
}

type shrregProcSlotT struct {
	pid         int32
	hostpid     int32
	used        [16]deviceMemory
	monitorused [16]uint64
	status      int32
}

type uuid struct {
	uuid [96]byte
}

type semT struct {
	sem [32]byte
}

type sharedRegionT struct {
	initializedFlag int32
	smInitFlag      int32
	ownerPid        uint32
	sem             semT
	num             uint64
	uuids           [16]uuid

	limit   [16]uint64
	smLimit [16]uint64
	procs   [1024]shrregProcSlotT

	procnum           int32
	utilizationSwitch int32
	recentKernel      int32
	priority          int32
}

type nvidiaCollector struct {
	// Exposed for testing
	cudevshrPath string
	at           *mmap.ReaderAt
	cudaCache    *sharedRegionT
}

func mmapcachefile(filename string, nc *nvidiaCollector) error {
	var m = &sharedRegionT{}
	f, err := os.OpenFile(filename, os.O_RDWR, 0666)
	if err != nil {
		fmt.Println("openfile error=", err.Error())
		return err
	}
	data, err := syscall.Mmap(int(f.Fd()), 0, int(unsafe.Sizeof(*m)), syscall.PROT_WRITE|syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return err
	}
	var cachestr *sharedRegionT = *(**sharedRegionT)(unsafe.Pointer(&data))
	fmt.Println("sizeof=", unsafe.Sizeof(*m), "cachestr=", cachestr.utilizationSwitch, cachestr.recentKernel)
	nc.cudaCache = cachestr
	return nil
}

func getvGPUMemoryInfo(nc *nvidiaCollector) (*sharedRegionT, error) {
	if len(nc.cudevshrPath) > 0 {
		if nc.cudaCache == nil {
			mmapcachefile(nc.cudevshrPath, nc)
		}
		return nc.cudaCache, nil
	}
	return &sharedRegionT{}, errors.New("not found path")
}
