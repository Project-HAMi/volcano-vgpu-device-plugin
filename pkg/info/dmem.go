// SPDX-License-Identifier: Apache-2.0

package info

import (
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// NvidiaVidmemCapacity reads the NVIDIA video memory regions registered at the
// cgroup root. Keys are PCI addresses and capacities are in bytes. An older
// kernel without dmem.capacity has no regions to report.
func NvidiaVidmemCapacity(root fs.FS) (map[string]uint64, error) {
	data, err := fs.ReadFile(root, "dmem.capacity")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	regions := make(map[string]uint64)
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		parts := strings.Split(fields[0], "/")
		if len(parts) != 3 || parts[0] != "nvidia" || parts[2] != "vidmem" {
			continue
		}
		if parts[1] == "" || len(fields) != 2 {
			return nil, fmt.Errorf("invalid NVIDIA dmem.capacity entry %q", strings.TrimSpace(line))
		}
		size, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid capacity for %s: %w", fields[0], err)
		}
		regions[parts[1]] = size
	}
	return regions, nil
}
