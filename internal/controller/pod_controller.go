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
	// Prometheus metrics (exposed on the manager's /metrics endpoint).
	reapedPods = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "terminating_pod_reaper_pods_force_deleted_total",
			Help: "Number of pods force-deleted from the Terminating state.",
		},
		[]string{"namespace"},
	)
	reapErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "terminating_pod_reaper_delete_errors_total",
			Help: "Number of errors while force-deleting stuck pods.",
		},
		[]string{"namespace"},
	)
	reapSkipped = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "terminating_pod_reaper_pods_skipped_total",
			Help: "Number of stuck pods skipped by filters or deferred by the deletion rate limit (see the reason label).",
		},
		[]string{"namespace", "reason"},
	)
	// Gauge: current number of pods stuck due to finalizers (not an event counter).
	reapFinalizerBlocked = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "terminating_pod_reaper_pods_finalizer_blocked",
			Help: "Current number of pods that outlived their grace period but are held by finalizers (force-delete can't help; manual intervention is required).",
		},
		[]string{"namespace"},
	)
)

func init() {
	metrics.Registry.MustRegister(reapedPods, reapErrors, reapSkipped, reapFinalizerBlocked)
}

// PodReaper force-deletes pods that have outlived their terminationGracePeriodSeconds
// (i.e. are stuck in the Terminating state).
type PodReaper struct {
	client.Client
	DryRun bool
	Filter *Filter

	// ExtraGrace is an extra buffer on top of the pod's terminationGracePeriodSeconds
	// (i.e. on top of deletionTimestamp) before a pod is considered stuck.
	//
	// deletionTimestamp is the deadline for a GRACEFUL shutdown, not a guarantee
	// that kubelet has already freed the node's resources by that moment: the
	// container runtime, a sidecar (classically an Istio proxy, which doesn't
	// always react to SIGTERM instantly), or a loaded node can legitimately
	// finish seconds to tens of seconds after the nominal deadline — that's
	// normal, not "stuck". Without a buffer, force-delete at T+0 systematically
	// races the legitimate (slightly slower) kubelet cleanup and wins: the pod
	// object disappears from the API before kubelet has actually killed the
	// containers, which can briefly orphan resources on the node. The buffer
	// gives kubelet a fair chance to finish on its own; the operator only steps
	// in if the pod outlived this extra margin too — i.e. it's genuinely stuck
	// (dead node, a finalizer that never clears), not just a bit slower than usual.
	ExtraGrace time.Duration

	// MaxDeletionsPerWindow caps the number of force-deletes per Window.
	// 0 or less means no limit.
	MaxDeletionsPerWindow int
	// Window is the rate limiter's window size. Independent of the manager's cache
	// resync period — a short Window lets a large backlog (e.g. after a zone
	// failure) drain quickly without having to also resync the cache more often.
	Window time.Duration

	mu              sync.Mutex
	windowStart     time.Time
	deletedInWindow int
	blocked         map[types.NamespacedName]string // stuck due to finalizers -> namespace
}

// allowDeletion reserves a deletion slot in the current window.
// Returns (false, timeUntilWindowReset) if the limit has been exhausted.
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

// markBlocked/clearBlocked maintain the "pods currently held by finalizers" gauge
// with dedup by pod name (the counter isn't bumped again by repeat events).
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

	// Metadata-only: only pod metadata is cached (deletionTimestamp,
	// ownerReferences, labels, finalizers, uid) — an order of magnitude less
	// memory than full Pod objects with spec/status.
	pod := &metav1.PartialObjectMetadata{}
	pod.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Pod"))
	if err := r.Get(ctx, req.NamespacedName, pod); err != nil {
		// The pod is gone — goal achieved; drop it from finalizer-blocked tracking.
		r.clearBlocked(req.NamespacedName)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// We only care about pods in the Terminating state.
	if pod.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Filter by owner / pod labels / namespace name and labels.
	if r.Filter != nil {
		// Only pods managed by an allowed controller kind (ReplicaSet/Job etc.),
		// which will recreate them in a live zone. StatefulSet/DaemonSet/bare pods
		// are left alone.
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
				// Couldn't read the namespace — retry later rather than delete blindly.
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			nsLabels = ns.Labels
		}
		if !r.Filter.NamespaceAllowed(pod.Namespace, nsLabels) {
			reapSkipped.WithLabelValues(pod.Namespace, "namespace").Inc()
			return ctrl.Result{}, nil
		}
	}

	// Заводим нулевую серию для этого namespace в обеих метриках прямо здесь,
	// а не только в момент первой ошибки/finalizer-блокировки: CounterVec и
	// GaugeVec из client_golang не публикуют серию по метке, пока WithLabelValues
	// ни разу не был вызван, так что без этого "проблем не было" неотличимо от
	// "метрика для этого namespace вообще не собирается" — оба случая дают
	// в Grafana "No data" вместо честного 0.
	reapErrors.WithLabelValues(pod.Namespace).Add(0)
	reapFinalizerBlocked.WithLabelValues(pod.Namespace).Add(0)

	// deletionTimestamp = deletionRequestTime + terminationGracePeriodSeconds,
	// i.e. the deadline for a graceful shutdown. We act no earlier than deadline +
	// ExtraGrace — see the comment on the ExtraGrace field about racing kubelet.
	deadline := pod.DeletionTimestamp.Time.Add(r.ExtraGrace)
	if remaining := time.Until(deadline); remaining > 0 {
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	l = l.WithValues("pod", req.String(),
		"terminatingSince", pod.DeletionTimestamp.Time.UTC().Format(time.RFC3339),
		"extraGrace", r.ExtraGrace.String())

	// The pod outlived its grace period (plus the buffer) — kubelet should have
	// removed it by now but didn't. If the pod is held by finalizers, force-delete
	// (grace=0) can't help: it only strips pods from an unreachable node, it
	// doesn't remove an object blocked by a finalizer. We only record such cases
	// via a gauge metric — auto-clearing finalizers would be dangerous.
	if len(pod.Finalizers) > 0 {
		if r.markBlocked(req.NamespacedName) {
			l.Info("pod stuck in Terminating due to finalizers; force-delete won't help — finalizers need to be cleared manually",
				"finalizers", pod.Finalizers)
		}
		return ctrl.Result{}, nil
	}
	// Finalizers cleared (or there never were any) — if previously marked blocked, unmark it.
	r.clearBlocked(req.NamespacedName)

	if r.DryRun {
		l.Info("dry-run: pod outlived its grace period, would have been force-deleted")
		return ctrl.Result{}, nil
	}

	// Deletion rate limit per window: protects the API server and gives
	// controllers time to recreate pods in waves instead of all at once.
	if ok, retryIn := r.allowDeletion(); !ok {
		reapSkipped.WithLabelValues(pod.Namespace, "rate_limited").Inc()
		l.Info("deletion rate limit exhausted for this window, pod will be deleted in the next window",
			"retryIn", retryIn.Round(time.Second).String())
		return ctrl.Result{RequeueAfter: retryIn}, nil
	}

	// Force-delete: grace-period=0. The UID precondition guards against a race —
	// we won't accidentally delete a new pod with the same name if the old one is already gone.
	gracePeriod := int64(0)
	uid := pod.UID
	err := r.Delete(ctx, pod, &client.DeleteOptions{
		GracePeriodSeconds: &gracePeriod,
		Preconditions:      &metav1.Preconditions{UID: &uid},
	})
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			return ctrl.Result{}, nil // already deleted between Get and Delete
		}
		reapErrors.WithLabelValues(pod.Namespace).Inc()
		l.Error(err, "failed to force-delete stuck pod")
		return ctrl.Result{}, err // controller-runtime will retry with backoff
	}

	reapedPods.WithLabelValues(pod.Namespace).Inc()
	l.Info("pod force-deleted from Terminating")
	return ctrl.Result{}, nil
}

// terminatingPredicate lets through events only for pods with a deletionTimestamp,
// so reconcile isn't triggered on every ordinary pod update. Delete events for
// terminating pods are needed to clean up finalizer-blocked tracking.
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
