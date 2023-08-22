package leaderelection

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8s "k8s.io/client-go/kubernetes"
)

const (
	defaultLeaseName     = "leader-election-lease"
	defaultLeaseDuration = 60 * time.Second
	defaultRenewDeadline = 30 * time.Second
	defaultRetryPeriod   = 15 * time.Second

	timeoutRunLeaderEngine     = 15 * time.Second
	defaultRandomizationFactor = 0.5
)

var (
	GetAPIClient = func() (k8s.Interface, error) {
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get in-cluster config, err: %v", err)
		}

		apiClient, err := k8s.NewForConfig(config)
		if err != nil {
			return nil, fmt.Errorf("failed to obtain K8s API client, err: %v", err)
		}
		return apiClient, nil
	}
	ErrNotRunning = fmt.Errorf("leader engine is not running")
)

var (
	initialRetryDelay = 1 * time.Second
	maxRetryDelay     = 5 * time.Minute
)

func WithLeaseDuration(dur time.Duration) K8sLeaderEngineOption {
	return func(le *K8sLeaderEngine) {
		le.leaseDuration = dur
	}
}

func WithRenewTime(dur time.Duration) K8sLeaderEngineOption {
	return func(le *K8sLeaderEngine) {
		le.renewDeadline = dur
	}
}

func WithRetryPeriod(dur time.Duration) K8sLeaderEngineOption {
	return func(le *K8sLeaderEngine) {
		le.retryPeriod = dur
	}
}

func WithHolderIdentity(identity string) K8sLeaderEngineOption {
	return func(le *K8sLeaderEngine) {
		le.holderIdentity = identity
	}
}

func WithLeaseName(name string) K8sLeaderEngineOption {
	return func(le *K8sLeaderEngine) {
		le.leaseName = name
	}
}

func WithLeaseNamespace(namespace string) K8sLeaderEngineOption {
	return func(le *K8sLeaderEngine) {
		le.leaseNamespace = namespace
	}
}

func WithContext(ctx context.Context) K8sLeaderEngineOption {
	return func(le *K8sLeaderEngine) {
		le.parentCtx = ctx
	}
}

func WithLogger(logger Logger) K8sLeaderEngineOption {
	return func(le *K8sLeaderEngine) {
		le.logger = logger
	}
}

func WithErrorLogger(logger Logger) K8sLeaderEngineOption {
	return func(le *K8sLeaderEngine) {
		le.errorLogger = logger
	}
}

func New(opts ...K8sLeaderEngineOption) (*K8sLeaderEngine, error) {
	e := &K8sLeaderEngine{
		leaseName:     defaultLeaseName,
		leaseDuration: defaultLeaseDuration,
		renewDeadline: defaultRenewDeadline,
		retryPeriod:   defaultRetryPeriod,
		logger:        &defaultLogger{},
		errorLogger:   &defaultLogger{},
		stopped:       make(chan struct{}),
	}

	for _, o := range opts {
		o(e)
	}

	e.leaseNamespace = e.getResourceNamespace()
	if e.parentCtx == nil {
		e.parentCtx = context.Background()
	}

	e.ctx, e.ctxCancel = context.WithCancel(e.parentCtx)
	err := doWithExpBackoff(e.initializeLeaderEngine, initialRetryDelay, maxRetryDelay)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize leader engine, err: %v", err)
	}

	e.logger.Log("Initialized leader engine with lease name: %q, namespace: %q", e.leaseName, e.leaseNamespace)
	return e, nil
}

// Start the leader engine and block until a leader is elected.
func (le *K8sLeaderEngine) Start() error {
	if !atomic.CompareAndSwapInt32(&le.running, 0, 1) {
		return fmt.Errorf("leader engine is already started")
	}

	go le.runLeaderEngine()
	timeout := time.After(timeoutRunLeaderEngine)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			leaderIdentity := le.GetLeader()
			if leaderIdentity != "" {
				le.logger.Log("Leader election started. Current leader is %q", leaderIdentity)
				return nil
			}
		case <-timeout:
			return fmt.Errorf("leader check timed out after %s. Stop the engine and try again", timeoutRunLeaderEngine)
		}
	}
}

// Stop the engine and wait until the goroutines have stopped
func (le *K8sLeaderEngine) Stop() error {
	if !atomic.CompareAndSwapInt32(&le.running, 1, 0) {
		return fmt.Errorf("leader engine is not started or already stopped")
	}

	le.ctxCancel()
	le.logger.Log("Leader election engine cancelled internal context, will wait until stopped signal is recieved")
	<-le.stopped
	le.logger.Log("Leader election engine stopped")
	return nil
}

func (le *K8sLeaderEngine) GetLeader() string {
	le.leaderIdentityMutex.Lock()
	defer le.leaderIdentityMutex.Unlock()
	return le.currentLeaderIdentity
}

func (le *K8sLeaderEngine) IsLeader() bool {
	return le.holderIdentity == le.GetLeader()
}

func (le *K8sLeaderEngine) initializeLeaderEngine() error {
	var err error
	if le.holderIdentity == "" {
		le.holderIdentity, err = os.Hostname()
		if err != nil {
			return fmt.Errorf("failed to obtain hostname, err: %v", err)
		}
	}

	le.logger.Log("Initializing leader engine with holder identity %q", le.holderIdentity)

	apiClient, err := GetAPIClient()
	if err != nil {
		return err
	}
	le.apiClient = apiClient
	le.coreClient = apiClient.CoreV1()
	le.coordinationClient = apiClient.CoordinationV1()
	_, err = le.coordinationClient.Leases(le.leaseNamespace).Get(le.parentCtx, le.leaseName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to obtain leases from namespace %q, err: %v", le.leaseNamespace, err)
	}

	le.leaderElector, err = le.newLeaderElector(le.parentCtx)
	if err != nil {
		return fmt.Errorf("failed to initialize leader elector process, err: %v", err)
	}
	le.logger.Log("Leader election for %q is initialized", le.holderIdentity)
	return nil
}

func (le *K8sLeaderEngine) runLeaderEngine() {
	for {
		le.logger.Log("Starting leader election process for %q under namespace %q", le.holderIdentity, le.leaseNamespace)
		le.leaderElector.Run(le.ctx)
		select {
		case <-le.ctx.Done():
			le.logger.Log("Leader election engine is stopping")
			le.stopped <- struct{}{}
			return
		default:
			le.logger.Log("%q lost the leader lease", le.holderIdentity)
		}
	}
}

func (le *K8sLeaderEngine) getResourceNamespace() string {
	namespace := os.Getenv("K8S_RESOURCE_NAMESPACE")
	if namespace != "" {
		return namespace
	}
	le.logger.Log("Environment variable K8S_RESOURCE_NAMESPACE is unset. Falling back to service account's namespace")
	return le.getLocalResourceNamespace()
}

func (le *K8sLeaderEngine) getLocalResourceNamespace() string {
	namespaceFilePath := filepath.Join(ServiceAccountMountPath, "namespace")
	if _, err := os.Stat(namespaceFilePath); os.IsNotExist(err) {
		le.logger.Log("Namespace file %q is not found. Will use 'default' namespace", namespaceFilePath)
		return "default"
	}
	ns, err := os.ReadFile(namespaceFilePath)
	if err != nil {
		le.logger.Log("Failed to access namespace file %q, err: %v. Will use 'default' namespace.", err)
		return "default"
	}
	return string(ns)
}

func doWithExpBackoff(f func() error, initialInterval, timeout time.Duration) error {
	b := backoff.NewExponentialBackOff()
	b.RandomizationFactor = defaultRandomizationFactor
	b.MaxElapsedTime = timeout
	b.InitialInterval = initialInterval
	return backoff.Retry(f, b)
}
