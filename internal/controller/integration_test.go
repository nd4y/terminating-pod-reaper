//go:build integration

// Интеграционные тесты против настоящего kube-apiserver (envtest).
// Запуск: KUBEBUILDER_ASSETS=$(setup-envtest use -p path) go test -tags=integration ./...
package controller

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
)

func TestMain(m *testing.M) {
	env := &envtest.Environment{}
	var err error
	if cfg, err = env.Start(); err != nil {
		panic("не удалось стартовать envtest (задан ли KUBEBUILDER_ASSETS?): " + err.Error())
	}
	if k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme}); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = env.Stop()
	os.Exit(code)
}

// makePod создаёт под с заданным controller-owner и (опц.) finalizer.
func makePod(t *testing.T, name, ownerKind string, withFinalizer bool) *corev1.Pod {
	t.Helper()
	yes := true
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "apps/v1",
				Kind:       ownerKind,
				Name:       name + "-owner",
				UID:        types.UID(name + "-owner-uid"),
				Controller: &yes,
			}},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c", Image: "registry.k8s.io/pause:3.9"}},
		},
	}
	if withFinalizer {
		p.Finalizers = []string{"test.local/hold"}
	}
	if err := k8sClient.Create(context.Background(), p); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	return p
}

func reconcile(t *testing.T, r *PodReaper, name string) (requeueAfter time.Duration) {
	t.Helper()
	res, err := r.Reconcile(context.Background(),
		ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: name}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	return res.RequeueAfter
}

func exists(t *testing.T, name string) bool {
	t.Helper()
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: name}, &corev1.Pod{})
	if err == nil {
		return true
	}
	if client.IgnoreNotFound(err) == nil {
		return false
	}
	t.Fatalf("get pod: %v", err)
	return false
}

func newReaper() *PodReaper {
	f, _ := BuildFilter("", "", "", "", "", "ReplicaSet,Job")
	return &PodReaper{Client: k8sClient, Filter: f}
}

// removeFinalizers снимает finalizers, чтобы apiserver удалил под (очистка).
func removeFinalizers(name string) {
	var p corev1.Pod
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: name}, &p); err != nil {
		return
	}
	p.Finalizers = nil
	_ = k8sClient.Update(context.Background(), &p)
}

// В envtest нет kubelet, поэтому под, удалённый с grace>0 и без finalizer,
// остаётся в состоянии Terminating — это и позволяет проверить force-delete.
func TestIntegration_ForceDeletesExpiredReplicaSetPod(t *testing.T) {
	p := makePod(t, "rs-pod", "ReplicaSet", false)
	grace := int64(1)
	if err := k8sClient.Delete(context.Background(), p, &client.DeleteOptions{GracePeriodSeconds: &grace}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !exists(t, "rs-pod") {
		t.Skip("envtest удалил под до дедлайна — пропускаем")
	}

	r := newReaper()
	// До дедлайна grace — ждём (RequeueAfter > 0), под на месте.
	if ra := reconcile(t, r, "rs-pod"); ra <= 0 {
		t.Fatalf("ожидался RequeueAfter > 0 до дедлайна, got %v", ra)
	}
	time.Sleep(1500 * time.Millisecond)
	// После дедлайна — force-delete.
	reconcile(t, r, "rs-pod")
	if exists(t, "rs-pod") {
		t.Fatal("под должен быть удалён после истечения grace")
	}
}

func TestIntegration_SkipsStatefulSetPod(t *testing.T) {
	p := makePod(t, "ss-pod", "StatefulSet", false)
	grace := int64(1)
	_ = k8sClient.Delete(context.Background(), p, &client.DeleteOptions{GracePeriodSeconds: &grace})
	if !exists(t, "ss-pod") {
		t.Skip("envtest удалил под до дедлайна — пропускаем")
	}
	time.Sleep(1500 * time.Millisecond)

	before := testutil.ToFloat64(reapSkipped.WithLabelValues("default", "owner_kind"))
	reconcile(t, newReaper(), "ss-pod")
	after := testutil.ToFloat64(reapSkipped.WithLabelValues("default", "owner_kind"))

	if !exists(t, "ss-pod") {
		t.Fatal("под StatefulSet не должен удаляться")
	}
	if after <= before {
		t.Fatal("ожидался инкремент reaper_pods_skipped_total{reason=owner_kind}")
	}
	removeFinalizers("ss-pod")
}

func TestIntegration_FinalizerBlocked(t *testing.T) {
	p := makePod(t, "fin-pod", "ReplicaSet", true)
	grace := int64(0)
	_ = k8sClient.Delete(context.Background(), p, &client.DeleteOptions{GracePeriodSeconds: &grace})
	if !exists(t, "fin-pod") {
		t.Skip("под неожиданно удалён, несмотря на finalizer")
	}

	before := testutil.ToFloat64(reapFinalizerBlocked.WithLabelValues("default"))
	reconcile(t, newReaper(), "fin-pod")
	after := testutil.ToFloat64(reapFinalizerBlocked.WithLabelValues("default"))

	if !exists(t, "fin-pod") {
		t.Fatal("под с finalizer force-delete удалить не может — должен остаться")
	}
	if after <= before {
		t.Fatal("ожидался инкремент reaper_pods_finalizer_blocked_total")
	}
	removeFinalizers("fin-pod")
}
