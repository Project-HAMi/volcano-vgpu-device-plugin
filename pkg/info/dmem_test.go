// SPDX-License-Identifier: Apache-2.0

package info

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
)

func TestNvidiaVidmemCapacity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		want    map[string]uint64
		wantErr bool
	}{
		{name: "missing file"},
		{name: "other vendor", input: "drm/0000:01:00.0/vram0 123\n", want: map[string]uint64{}},
		{name: "mixed devices", input: "drm/0000:01:00.0/vram0 123\nnvidia/00000000:4B:00.0/vidmem 8589934592\nnvidia/00000000:4C:00.0/vidmem 0\n", want: map[string]uint64{"00000000:4B:00.0": 8589934592, "00000000:4C:00.0": 0}},
		{name: "missing size", input: "nvidia/00000000:4B:00.0/vidmem", wantErr: true},
		{name: "invalid size", input: "nvidia/00000000:4B:00.0/vidmem unknown", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := fstest.MapFS{}
			if tc.input != "" {
				root["dmem.capacity"] = &fstest.MapFile{Data: []byte(tc.input)}
			}
			got, err := NvidiaVidmemCapacity(root)
			if (err != nil) != tc.wantErr || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got (%v, %v), want (%v, error=%t)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestNvidiaVidmemCapacityMissingRoot(t *testing.T) {
	root := os.DirFS(filepath.Join(t.TempDir(), "missing"))
	_, err := NvidiaVidmemCapacity(root)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing cgroup root returned %v, want fs.ErrNotExist", err)
	}
}
