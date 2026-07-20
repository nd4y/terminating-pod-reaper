package controller

import (
	"context"
	"sync"
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
			Help: "Количество зависших подов, пропущенных фильтрами или отложенных лимитом удалений (см. label reason).",
		},
		[]string{"namespace", "reason"},
	)
	// Gauge: текущее число подов, застрявших из-за finalizers (не событий).
	reapFinalizerBlocked = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "terminating_pod_reaper_pods_finalizer_blocked",
			Help: "Текущее количество подов, переживших grace-период, но удерживаемых finalizers (force-delete бессилен, нужно ручное вмешательство).",
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

	// ExtraGrace — дополнительный буфер сверх terminationGracePeriodSeconds
	// пода (т.е. сверх deletionTimestamp), прежде чем считать под застрявшим.
	//
	// deletionTimestamp — это дедлайн ШТАТНОГО завершения, а не гарантия, что
	// kubelet уже освободил ресурсы ноды к этому моменту: контейнер runtime,
	// sidecar (классика — Istio proxy, не всегда мгновенно реагирующий на
	// SIGTERM) или загруженный узел могут закончить на секунды-десятки секунд
	// позже номинального дедлайна — это нормально, не «завис». Без буфера
	// force-delete в T+0 систематически гонится с законным (чуть более
	// медленным) завершением kubelet и побеждает: пода умирает вроде бы вовремя,
	// но объект в API исчезает до того, как kubelet реально прибил контейнеры,
	// что может ненадолго осиротить ресурсы на ноде. Буфер даёт kubelet
	// честный шанс закончить самому; вмешивается оператор только если под
	// пережил ещё и этот запас — то есть действительно застрял (мёртвая нода,
	// зависший finalizer), а не просто чуть медленнее обычного.
	ExtraGrace time.Duration

	// MaxDeletionsPerWindow ограничивает число force-delete за окно Window.
	// 0 или меньше — без ограничения.
	MaxDeletionsPerWindow int
	// Window — размер окна лимитера (обычно равен периоду ресинка кэша).
	Window time.Duration

	mu              sync.Mutex
	windowStart     time.Time
	deletedInWindow int
	blocked         map[types.NamespacedName]string // застрявшие из-за finalizers → namespace
}

// allowDeletion резервирует слот на удаление в текущем окне.
// Возвращает (false, времяДоСбросаОкна), если лимит исчерпан.
func (r *PodReaper) allowDeletion() (bool, time.Duration) {
	if r.MaxDeletionsPerWindow <= 0 {
		return true, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if r.windowStart.IsZero() || now.Sub(r.windowStart) >= r.Window {
		r.windowStart = now
		r.deletedInWindow = 0
	}
	if r.deletedInWindow < r.MaxDeletionsPerWindow {
		r.deletedInWindow++
		return true, 0
	}
	return false, r.windowStart.Add(r.Window).Sub(now)
}

// markBlocked/clearBlocked поддерживают gauge «сколько подов сейчас держат finalizers»
// с дедупликацией по имени пода (счётчик не накручивается повторными событиями).
func (r *PodReaper) markBlocked(nn types.NamespacedName) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.blocked == nil {
		r.blocked = map[types.NamespacedName]string{}
	}
	if _, ok := r.blocked[nn]; ok {
		return false
	}
	r.blocked[nn] = nn.Namespace
	reapFinalizerBlocked.WithLabelValues(nn.Namespace).Inc()
	return true
}

func (r *PodReaper) clearBlocked(nn types.NamespacedName) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ns, ok := r.blocked[nn]; ok {
		delete(r.blocked, nn)
		reapFinalizerBlocked.WithLabelValues(ns).Dec()
	}
}

// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

func (r *PodReaper) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx)

	// Metadata-only: кэшируются только метаданные подов (deletionTimestamp,
	// ownerReferences, labels, finalizers, uid) — на порядок меньше памяти,
	// чем полные объекты Pod со spec/status.
	pod := &metav1.PartialObjectMetadata{}
	pod.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Pod"))
	if err := r.Get(ctx, req.NamespacedName, pod); err != nil {
		// Под исчез — цель достигнута; убираем из учёта finalizer-blocked.
		r.clearBlocked(req.NamespacedName)
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
	// т.е. дедлайн штатной graceful-остановки. Действуем не раньше deadline +
	// ExtraGrace — см. комментарий у поля ExtraGrace про гонку с kubelet.
	deadline := pod.DeletionTimestamp.Time.Add(r.ExtraGrace)
	if remaining := time.Until(deadline); remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	l = l.WithValues("pod", req.String(),
		"terminatingSince", pod.DeletionTimestamp.Time.UTC().Format(time.RFC3339),
		"extraGrace", r.ExtraGrace.String())

	// Под пережил свой grace-период (плюс буфер) — kubelet должен был его убрать, но не убрал.
	// Если под держат finalizers, force-delete (grace=0) бессилен: он снимает лишь
	// поды с недоступной ноды, но не удаляет объект, заблокированный finalizer'ом.
	// Такие случаи только отмечаем gauge-метрикой — авто-снятие finalizers опасно.
	if len(pod.Finalizers) > 0 {
		if r.markBlocked(req.NamespacedName) {
			l.Info("под застрял в Terminating из-за finalizers; force-delete не поможет — нужно ручное снятие finalizers",
				"finalizers", pod.Finalizers)
		}
		return ctrl.Result{}, nil
	}
	// Finalizers сняты (или их не было) — если ранее числился заблокированным, убираем.
	r.clearBlocked(req.NamespacedName)

	if r.DryRun {
		l.Info("dry-run: под пережил grace-период, был бы удалён принудительно")
		return ctrl.Result{}, nil
	}

	// Лимит удалений за окно: защищает API-сервер и даёт время контроллерам
	// пересоздавать поды волнами, а не лавиной.
	if ok, retryIn := r.allowDeletion(); !ok {
		reapSkipped.WithLabelValues(pod.Namespace, "rate_limited").Inc()
		l.Info("лимит удалений за окно исчерпан, под будет удалён в следующем окне",
			"retryIn", retryIn.Round(time.Second).String())
		return ctrl.Result{RequeueAfter: retryIn}, nil
	}

	// Force-delete: grace-period=0. Precondition по UID защищает от гонки —
	// не удалим случайно новый под с тем же именем, если старый уже ушёл.
	gracePeriod := int64(0)
	uid := pod.UID
	err := r.Delete(ctx, pod, &client.DeleteOptions{
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
// чтобы не гонять reconcile на каждый обычный апдейт пода. Delete-события
// terminating-подов нужны для очистки учёта finalizer-blocked.
func terminatingPredicate() predicate.Predicate {
	isTerminating := func(o client.Object) bool {
		return o != nil && !o.GetDeletionTimestamp().IsZero()
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isTerminating(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isTerminating(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isTerminating(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return isTerminating(e.Object) },
	}
}

func (r *PodReaper) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}, builder.OnlyMetadata, builder.WithPredicates(terminatingPredicate())).
		Named("terminating-pod-reaper").
		Complete(r)
}
