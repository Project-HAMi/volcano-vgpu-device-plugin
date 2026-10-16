package nvidia

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"volcano.sh/k8s-device-plugin/pkg/nodepodinformer"
)

type countingRT struct {
	base  http.RoundTripper
	lists atomic.Int64
	watch atomic.Int64
}

func (c *countingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Path, "/pods") {
		if req.URL.Query().Get("watch") == "true" {
			c.watch.Add(1)
		} else if req.Method == http.MethodGet {
			c.lists.Add(1)
		}
	}
	return c.base.RoundTrip(req)
}

func liveClient(t *testing.T, ctr *countingRT) kubernetes.Interface {
	t.Helper()
	cfg, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	require.NoError(t, err)
	cfg.QPS = 50
	cfg.Burst = 100
	cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		ctr.base = rt
		return ctr
	})
	cs, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)
	return cs
}

func seedNodePods(t *testing.T, cs kubernetes.Interface, ns, node string, n int) {
	t.Helper()
	_, err := cs.CoreV1().Namespaces().Get(context.Background(), ns, metav1.GetOptions{})
	if err != nil {
		_, err = cs.CoreV1().Namespaces().Create(context.Background(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		}, metav1.CreateOptions{})
		require.NoError(t, err)
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("live-mon-%d", i)
		_, err := cs.CoreV1().Pods(ns).Get(context.Background(), name, metav1.GetOptions{})
		if err == nil {
			continue
		}
		_, err = cs.CoreV1().Pods(ns).Create(context.Background(), &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:   name,
				Labels: map[string]string{"app": "live-mon"},
			},
			Spec: corev1.PodSpec{
				NodeName:   node,
				Containers: []corev1.Container{{Name: "c", Image: "busybox:1.36"}},
			},
		}, metav1.CreateOptions{})
		require.NoError(t, err)
	}
}

func tickUpdate(l *ContainerLister, d, interval time.Duration) int {
	n := 0
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		_ = l.Update()
		n++
		time.Sleep(interval)
	}
	return n
}

// TestLiveMonitorInformerCutsPodList compares API Pod LIST volume for the
// legacy Update path versus the node-scoped informer wiring used in production.
// Requires KUBECONFIG and NODE_NAME.
func TestLiveMonitorInformerCutsPodList(t *testing.T) {
	if os.Getenv("KUBECONFIG") == "" || os.Getenv("NODE_NAME") == "" {
		t.Skip("KUBECONFIG and NODE_NAME required")
	}
	node := os.Getenv("NODE_NAME")
	ns := "monitor-opt-verify"
	root := t.TempDir()

	ctrBase := &countingRT{}
	csBase := liveClient(t, ctrBase)
	seedNodePods(t, csBase, ns, node, 15)
	ctrBase.lists.Store(0)
	ctrBase.watch.Store(0)
	lBase := &ContainerLister{
		containerPath: root,
		containers:    map[string]*ContainerUsage{},
		clientset:     csBase,
	}
	t.Cleanup(lBase.Close)
	ticks := tickUpdate(lBase, 25*time.Second, time.Second)
	baseLists := ctrBase.lists.Load()
	baseWatch := ctrBase.watch.Load()
	t.Logf("baseline ticks=%d pod_LIST=%d watch=%d LIST/tick=%.2f",
		ticks, baseLists, baseWatch, float64(baseLists)/float64(ticks))

	ctrOpt := &countingRT{}
	csOpt := liveClient(t, ctrOpt)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	inf, err := nodepodinformer.New(csOpt, node)
	require.NoError(t, err)
	inf.Start(ctx)
	syncCtx, syncCancel := context.WithTimeout(ctx, 30*time.Second)
	require.NoError(t, inf.WaitForSync(syncCtx))
	syncCancel()
	ctrOpt.lists.Store(0)
	ctrOpt.watch.Store(0)
	lOpt := &ContainerLister{
		containerPath: root,
		containers:    map[string]*ContainerUsage{},
		clientset:     csOpt,
	}
	t.Cleanup(lOpt.Close)
	lOpt.UseNodePodList(inf.List)
	ticksOpt := tickUpdate(lOpt, 25*time.Second, time.Second)
	optLists := ctrOpt.lists.Load()
	optWatch := ctrOpt.watch.Load()
	t.Logf("optimized ticks=%d pod_LIST=%d watch=%d LIST/tick=%.2f",
		ticksOpt, optLists, optWatch, float64(optLists)/float64(ticksOpt))

	require.GreaterOrEqual(t, baseLists, int64(ticks-2), "baseline should List roughly once per tick")
	require.Less(t, optLists, int64(3), "optimized steady Update should not List Pods from API")
	require.Less(t, optLists, baseLists/5)
}
