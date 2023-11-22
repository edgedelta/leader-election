package leaderelection

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/client-go/kubernetes"

	coordinationV1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	coreV1 "k8s.io/client-go/kubernetes/typed/core/v1"
	leaderelection "k8s.io/client-go/tools/leaderelection"
)

type NewLeaderPayload struct {
	CurrentlyLeading bool
	OldLeader        string
	NewLeader        string
}

type LeaderEngineSubscriber interface {
	Name() string
	NewLeader(payload *NewLeaderPayload) error
}

type K8sLeaderEngine struct {
	isRunning atomic.Bool
	stopped   chan struct{}

	parentCtx context.Context
	ctx       context.Context
	ctxCancel context.CancelFunc

	holderIdentity string
	leaseDuration  time.Duration
	leaseName      string
	leaseNamespace string
	renewDeadline  time.Duration
	retryPeriod    time.Duration

	apiClient             kubernetes.Interface
	coreClient            coreV1.CoreV1Interface
	coordinationClient    coordinationV1.CoordinationV1Interface
	leaderElector         *leaderelection.LeaderElector
	leaderIdentityMutex   sync.Mutex
	currentLeaderIdentity string

	subscribersMu sync.Mutex
	subscribers   map[string]LeaderEngineSubscriber

	logger      Logger
	errorLogger Logger
}

type K8sLeaderEngineOption func(*K8sLeaderEngine)

type Logger interface {
	Log(string, ...interface{})
}
