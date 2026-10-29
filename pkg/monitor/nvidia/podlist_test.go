package nvidia

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestUpdateUsesInjectedPodSnapshot(t *testing.T) {
	client := fake.NewClientset()
	lists := 0
	client.PrependReactor("list", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		lists++
		return true, &corev1.PodList{}, nil
	})
	l := &ContainerLister{
		containerPath: t.TempDir(),
		containers:    map[string]*ContainerUsage{},
		clientset:     client,
		listNodePods: func() ([]*corev1.Pod, error) {
			return []*corev1.Pod{{
				ObjectMeta: metav1.ObjectMeta{Name: "cached", UID: "uid-cached"},
			}}, nil
		},
	}

	require.NoError(t, l.Update())
	require.Zero(t, lists)
}
