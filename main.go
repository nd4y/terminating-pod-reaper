package main

import (
	"flag"
	"os"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/nd4y/terminating-pod-reaper/internal/controller"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
}

// envStr returns the env value if non-empty, otherwise the given default.
func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	var (
		metricsAddr    string
		probeAddr      string
		enableLeaderEl bool
		dryRun         bool
		namespacesFlag string

		nsIncludeRegex     string
		nsExcludeRegex     string
		nsIncludeSelector  string
		nsExcludeSelector  string
		podExcludeSelector string
		ownerKinds         string

		maxDeletions        int
		syncPeriodSecs      int
		extraGraceSecs      int
		rateLimitWindowSecs int
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Address for /metrics.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Address for health/readiness probes.")
	flag.BoolVar(&enableLeaderEl, "leader-elect", false, "Enable leader election for HA (2+ replicas).")
	flag.BoolVar(&dryRun, "dry-run", true,
		"Only log, never delete. Defaults to true (safe mode).")
	flag.StringVar(&namespacesFlag, "namespaces", "",
		"Hard-restrict the watch to this comma-separated list of namespaces. Empty means the whole cluster.")
	flag.StringVar(&nsIncludeRegex, "namespace-include-regex", "",
		"Only process namespaces whose name matches this regex.")
	flag.StringVar(&nsExcludeRegex, "namespace-exclude-regex", "^kube-system$",
		"Exclude namespaces whose name matches this regex. kube-system is protected by default.")
	flag.StringVar(&nsIncludeSelector, "namespace-include-selector", "",
		"Only process namespaces whose labels match this label selector (e.g. 'terminating-pod-reaper=enabled').")
	flag.StringVar(&nsExcludeSelector, "namespace-exclude-selector", "",
		"Exclude namespaces whose labels match this label selector.")
	flag.StringVar(&podExcludeSelector, "pod-exclude-selector", "",
		"Skip pods whose labels match this label selector (e.g. 'terminating-pod-reaper.io/ignore=true').")
	flag.StringVar(&ownerKinds, "reap-owner-kinds", "ReplicaSet,Job",
		"Only delete pods managed by a controller with one of these Kinds (comma-separated). "+
			"Empty means any owner, including StatefulSet and bare pods.")
	flag.IntVar(&maxDeletions, "max-deletions-per-interval", 200,
		"Maximum number of pods deleted per rate-limit-window-seconds window. 0 means no limit.")
	flag.IntVar(&syncPeriodSecs, "sync-period-seconds", 600,
		"How often (seconds) to fully resync cluster state — a safety net against missed watch "+
			"events. Independent of the deletion rate limit. Defaults to 600 (10 min).")
	flag.IntVar(&rateLimitWindowSecs, "rate-limit-window-seconds", 30,
		"Window (seconds) for the deletion rate limit (max-deletions-per-interval). Independent of "+
			"sync-period-seconds — a short window lets the operator clear a large backlog quickly "+
			"(e.g. after a zone failure) while the cache resync stays infrequent. Defaults to 30.")
	flag.IntVar(&extraGraceSecs, "extra-grace-seconds", 60,
		"Extra buffer (seconds) on top of a pod's terminationGracePeriodSeconds before force-delete. "+
			"Gives kubelet a fair chance to finish the pod itself (including sidecar containers like the "+
			"Istio proxy, which can need a bit more than the nominal grace period) — without this buffer "+
			"the operator systematically races the normal shutdown. Defaults to 60.")
	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Env takes priority over flag defaults (convenient for manifest-based deploys).
	// For parameters with a non-empty default we use LookupEnv: an empty env value
	// is meaningful and should override the default (so Helm stays authoritative).
	if v, ok := os.LookupEnv("DRY_RUN"); ok {
		b, err := strconv.ParseBool(v)
		if err != nil {
			// Unparseable value for the safety switch -> stay in dry-run.
			setupLog.Info("could not parse DRY_RUN, staying in dry-run", "value", v)
			b = true
		}
		dryRun = b
	}
	namespacesFlag = envStr("NAMESPACES", namespacesFlag)
	nsIncludeRegex = envStr("NAMESPACE_INCLUDE_REGEX", nsIncludeRegex)
	if v, ok := os.LookupEnv("NAMESPACE_EXCLUDE_REGEX"); ok {
		nsExcludeRegex = v
	}
	nsIncludeSelector = envStr("NAMESPACE_INCLUDE_SELECTOR", nsIncludeSelector)
	nsExcludeSelector = envStr("NAMESPACE_EXCLUDE_SELECTOR", nsExcludeSelector)
	podExcludeSelector = envStr("POD_EXCLUDE_SELECTOR", podExcludeSelector)
	if v, ok := os.LookupEnv("REAP_OWNER_KINDS"); ok {
		ownerKinds = v
	}
	if v, ok := os.LookupEnv("MAX_DELETIONS_PER_INTERVAL"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			setupLog.Error(err, "invalid MAX_DELETIONS_PER_INTERVAL", "value", v)
			os.Exit(1)
		}
		maxDeletions = n
	}
	if v, ok := os.LookupEnv("SYNC_PERIOD_SECONDS"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			setupLog.Error(err, "invalid SYNC_PERIOD_SECONDS (must be > 0)", "value", v)
			os.Exit(1)
		}
		syncPeriodSecs = n
	}
	if v, ok := os.LookupEnv("EXTRA_GRACE_SECONDS"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			setupLog.Error(err, "invalid EXTRA_GRACE_SECONDS (must be >= 0)", "value", v)
			os.Exit(1)
		}
		extraGraceSecs = n
	}
	if v, ok := os.LookupEnv("RATE_LIMIT_WINDOW_SECONDS"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			setupLog.Error(err, "invalid RATE_LIMIT_WINDOW_SECONDS (must be > 0)", "value", v)
			os.Exit(1)
		}
		rateLimitWindowSecs = n
	}
	if syncPeriodSecs <= 0 {
		setupLog.Error(nil, "sync-period-seconds must be > 0", "value", syncPeriodSecs)
		os.Exit(1)
	}
	if extraGraceSecs < 0 {
		setupLog.Error(nil, "extra-grace-seconds must be >= 0", "value", extraGraceSecs)
		os.Exit(1)
	}
	if rateLimitWindowSecs <= 0 {
		setupLog.Error(nil, "rate-limit-window-seconds must be > 0", "value", rateLimitWindowSecs)
		os.Exit(1)
	}
	syncPeriod := time.Duration(syncPeriodSecs) * time.Second
	extraGrace := time.Duration(extraGraceSecs) * time.Second
	rateLimitWindow := time.Duration(rateLimitWindowSecs) * time.Second

	if dryRun {
		setupLog.Info("DRY-RUN MODE enabled: pods will NOT be deleted, logging only. " +
			"For real deletion set dry-run=false (Helm: --set config.dryRun=false)")
	}

	filter, err := controller.BuildFilter(
		nsIncludeRegex, nsExcludeRegex,
		nsIncludeSelector, nsExcludeSelector,
		podExcludeSelector,
		ownerKinds,
	)
	if err != nil {
		setupLog.Error(err, "invalid filter parameters")
		os.Exit(1)
	}

	// Restrict the cache/watch to specific namespaces, if set.
	// SyncPeriod is the full resync (poll) period for cluster state.
	cacheOpts := cache.Options{SyncPeriod: &syncPeriod}
	if namespacesFlag != "" {
		nsMap := map[string]cache.Config{}
		for _, ns := range strings.Split(namespacesFlag, ",") {
			ns = strings.TrimSpace(ns)
			if ns != "" {
				nsMap[ns] = cache.Config{}
			}
		}
		cacheOpts.DefaultNamespaces = nsMap
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Cache:                  cacheOpts,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderEl,
		LeaderElectionID:       "terminating-pod-reaper.nd4y.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	if err = (&controller.PodReaper{
		Client:                mgr.GetClient(),
		DryRun:                dryRun,
		Filter:                filter,
		ExtraGrace:            extraGrace,
		MaxDeletionsPerWindow: maxDeletions,
		Window:                rateLimitWindow,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to start controller")
		os.Exit(1)
	}

	_ = mgr.AddHealthzCheck("healthz", healthz.Ping)
	_ = mgr.AddReadyzCheck("readyz", healthz.Ping)

	setupLog.Info("starting terminating-pod-reaper",
		"dryRun", dryRun,
		"namespaces", namespacesFlag,
		"nsIncludeRegex", nsIncludeRegex,
		"nsExcludeRegex", nsExcludeRegex,
		"nsIncludeSelector", nsIncludeSelector,
		"nsExcludeSelector", nsExcludeSelector,
		"podExcludeSelector", podExcludeSelector,
		"ownerKinds", ownerKinds,
		"maxDeletionsPerInterval", maxDeletions,
		"rateLimitWindow", rateLimitWindow.String(),
		"syncPeriod", syncPeriod.String(),
		"extraGrace", extraGrace.String())

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "manager exited with an error")
		os.Exit(1)
	}
}
