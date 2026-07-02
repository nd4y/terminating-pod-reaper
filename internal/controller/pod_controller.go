package controller

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var (
	// Prometheus-метрики (доступны на /metrics менеджера).
	reapedPods = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "terminating_pod_reaper_pods_force_deleted_total",
			Help: "Количество подов, принудительно удалённых из состояния Terminating.",
		},
		[]string{"namespace"},
	)
	reapErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "terminating_pod_reaper_delete_errors_total",
			Help: "Количество ошибок при force-delete зависших подов.",
		},
		[]string{"namespace"},
	)
	reapSkipped = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "terminating_pod_reaper_pods_skipped_total",
			Help: "Количество зависших подов, пропущенных из-за фильтров.",
		},
		[]string{"namespace", "reason"},
	)
	reapFinalizerBlocked = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "terminating_pod_reaper_pods_finalizer_blocked_total",
			Help: "Сколько раз обнаружен под, переживший grace-период, но удерживаемый finalizers (force-delete бессилен, нужно ручное вмешательство).",
		},
		[]string{"namespace"},
	)
)

func init() {
	metrics.Registry.MustRegister(reapedPods, reapErrors, reapSkipped, reapFinalizerBlocked)
}

// PodReaper принудительно удаляет поды, пережившие свой terminationGracePeriodSeconds
// (т.е. застрявшие в состоянии Terminating).
type PodReaper struct {
	client.Client
	DryRun bool
	Filter *Filter
}

// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

func (r *PodReaper) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		// Под уже исчез — цель достигнута.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Нас интересуют только поды в состоянии Terminating.
	if pod.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Фильтрация по владельцу / меткам пода / имени и меткам namespace.
	if r.Filter != nil {
		// Только поды под управлением разрешённого контроллера (ReplicaSet/Job и т.п.),
		// который пересоздаст их в живой зоне. StatefulSet/DaemonSet/«голые» — не трогаем.
		if !r.Filter.OwnerAllowed(pod.OwnerReferences) {
			reapSkipped.WithLabelValues(pod.Namespace, "owner_kind").Inc()
			return ctrl.Result{}, nil
		}
		if !r.Filter.PodAllowed(pod.Labels) {
			reapSkipped.WithLabelValues(pod.Namespace, "pod_label").Inc()
			return ctrl.Result{}, nil
		}

		var nsLabels map[string]string
		if r.Filter.NeedsNamespaceLabels() {
			var ns corev1.Namespace
			if err := r.Get(ctx, types.NamespacedName{Name: pod.Namespace}, &ns); err != nil {
				// Не смогли прочитать namespace — повторим позже, чем удалять вслепую.
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			nsLabels = ns.Labels
		}
		if !r.Filter.NamespaceAllowed(pod.Namespace, nsLabels) {
			reapSkipped.WithLabelValues(pod.Namespace, "namespace").Inc()
			return ctrl.Result{}, nil
		}
	}

	// deletionTimestamp = времяЗапросаУдаления + terminationGracePeriodSeconds,
	// т.е. это дедлайн штатной graceful-остановки. Пока он не наступил — под
	// находится в своём законном grace-периоде, трогать его небезопасно: ждём.
	if remaining := time.Until(pod.DeletionTimestamp.Time); remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	l = l.WithValues("pod", req.String(),
		"terminatingSince", pod.DeletionTimestamp.Time.UTC().Format(time.RFC3339))

	// Под пережил свой grace-период — kubelet должен был его убрать, но не убрал.
	// Если под держат finalizers, force-delete (grace=0) бессилен: он снимает лишь
	// поды с недоступной ноды, но не удаляет объект, заблокированный finalizer'ом.
	// Такие случаи только отмечаем метрикой — авто-снятие finalizers слишком опасно.
	if len(pod.Finalizers) > 0 {
		reapFinalizerBlocked.WithLabelValues(pod.Namespace).Inc()
		l.Info("под застрял в Terminating из-за finalizers; force-delete не поможет — нужно ручное снятие finalizers",
			"finalizers", pod.Finalizers)
		return ctrl.Result{}, nil
	}

	if r.DryRun {
		l.Info("dry-run: под пережил grace-период, был бы удалён принудительно")
		return ctrl.Result{}, nil
	}

	// Force-delete: grace-period=0. Precondition по UID защищает от гонки —
	// не удалим случайно новый под с тем же именем, если старый уже ушёл.
	gracePeriod := int64(0)
	uid := pod.UID
	err := r.Delete(ctx, &pod, &client.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		Preconditions:      &metav1.Preconditions{UID: &uid},
	})
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			return ctrl.Result{}, nil // уже удалён между Get и Delete
		}
		reapErrors.WithLabelValues(pod.Namespace).Inc()
		l.Error(err, "не удалось принудительно удалить зависший под")
		return ctrl.Result{}, err // controller-runtime повторит с backoff
	}

	reapedPods.WithLabelValues(pod.Namespace).Inc()
	l.Info("под принудительно удалён из Terminating")
	return ctrl.Result{}, nil
}

// terminatingPredicate пропускает события только для подов с deletionTimestamp,
// чтобы не гонять reconcile на каждый обычный апдейт пода.
func terminatingPredicate() predicate.Predicate {
	isTerminating := func(o client.Object) bool {
		return o != nil && !o.GetDeletionTimestamp().IsZero()
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isTerminating(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isTerminating(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return isTerminating(e.Object) },
	}
}

func (r *PodReaper) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}, builder.WithPredicates(terminatingPredicate())).
		Named("terminating-pod-reaper").
		Complete(r)
}
