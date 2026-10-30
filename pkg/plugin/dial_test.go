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

package plugin

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
)

func TestDial(t *testing.T) {
	plugin := &nvidiaDevicePlugin{ctx: context.Background()}
	sock := filepath.Join(t.TempDir(), "p.sock")

	t.Run("missing socket times out", func(t *testing.T) {
		start := time.Now()
		if c, err := plugin.dial(sock, 300*time.Millisecond); err == nil {
			_ = c.Close()
			t.Fatal("expected error for missing socket")
		}
		if time.Since(start) > 3*time.Second {
			t.Fatal("dial did not honor the timeout")
		}
	})

	t.Run("listening socket connects", func(t *testing.T) {
		lis, err := net.Listen("unix", sock)
		if err != nil {
			t.Fatal(err)
		}
		srv := grpc.NewServer()
		go func() { _ = srv.Serve(lis) }()
		defer srv.Stop()

		c, err := plugin.dial(sock, 3*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		_ = c.Close()
	})
}
