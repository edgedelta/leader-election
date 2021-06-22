package leaderelection

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes/fake"

	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLeaderEngineError(t *testing.T) {
	currentRetryDelay := maxRetryDelay
	maxRetryDelay = 5 * time.Second
	defer func() {
		maxRetryDelay = currentRetryDelay
	}()
	_, err := New()
	if err == nil {
		t.Errorf("Failed to obtain timeout error")
	}
}

func TestLeaseAcquirement(t *testing.T) {
	const leaseName = "edgedelta-leader-election-lease-test"
	const leaseNamespace = "test-namespace"
	const holderIdentity = "test-identity"

	client := fake.NewSimpleClientset()
	ctx, cancel := context.WithCancel(context.Background())
	le := &K8sLeaderEngine{
		holderIdentity:     holderIdentity,
		leaseName:          leaseName,
		leaseNamespace:     leaseNamespace,
		leaseDuration:      1 * time.Second,
		renewDeadline:      500 * time.Millisecond,
		retryPeriod:        200 * time.Millisecond,
		apiClient:          client,
		coreClient:         client.CoreV1(),
		coordinationClient: client.CoordinationV1(),
		logger:             &testLogger{t: t},
		errorLogger:        &testLogger{t: t},
		parentCtx:          context.Background(),
		ctx:                ctx,
		ctxCancel:          cancel,
		stopped:            make(chan struct{}),
	}

	_, err := le.coordinationClient.Leases(leaseNamespace).Get(ctx, le.leaseName, metaV1.GetOptions{})
	if !errors.IsNotFound(err) {
		t.Errorf("Had an error whilst obtaining current leases, err: %v", err)
	}

	le.leaderElector, err = le.newLeaderElector(ctx)
	if err != nil {
		t.Errorf("Had an error whilst creating a leader elector, err: %v", err)
	}

	lease, err := le.coordinationClient.Leases(leaseNamespace).Get(ctx, le.leaseName, metaV1.GetOptions{})
	if err != nil {
		t.Errorf("Had an error whilst obtaining current leases, err: %v", err)
	}
	if lease.Name != leaseName {
		t.Errorf("Lease names does not match, wanted %q, obtained %q", leaseName, lease.Name)
	}
	if lease.Name != leaseName {
		t.Errorf("Lease names does not match, wanted %q, obtained %q", leaseName, lease.Name)
	}

	if err := le.Start(); err != nil {
		t.Errorf("Failed to start leader election engine, err: %v", err)
	}

	lease, err = le.coordinationClient.Leases(leaseNamespace).Get(ctx, le.leaseName, metaV1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		t.Errorf("Had an error while obtaining current leases, err: %v", err)
	}
	if *lease.Spec.LeaseTransitions != 1 {
		t.Errorf("Number of lease transitions does not match, wanted %d, obtained %d", 1, *lease.Spec.LeaseTransitions)
	}
	if !le.IsLeader() {
		t.Errorf("Current client is not leader, leader is %q", le.GetLeader())
	}
	if err := le.Stop(); err != nil {
		t.Errorf("Failed to stop leader election engine, err: %v", err)
	}
}

type testLogger struct {
	t *testing.T
}

func (tl *testLogger) Log(format string, args ...interface{}) {
	tl.t.Logf(format, args...)
}

func TestRaceCondition(t *testing.T) {
	const leaseName = "leader-election-lease-test"
	const leaseNamespace = "test-namespace"
	const holderIdentity = "test-identity-"
	const number = 10

	client := fake.NewSimpleClientset()
	channelMap := map[string]chan struct{}{}
	leaderEngineMap := map[string]*K8sLeaderEngine{}
	for i := 0; i < number; i++ {
		identity := fmt.Sprintf("%s%d", holderIdentity, i)
		stopChannel := make(chan struct{}, 1)
		channelMap[identity] = stopChannel
		ctx, cancel := context.WithCancel(context.Background())
		le := &K8sLeaderEngine{
			holderIdentity:     identity,
			leaseName:          leaseName,
			leaseNamespace:     leaseNamespace,
			leaseDuration:      1 * time.Second,
			renewDeadline:      500 * time.Millisecond,
			retryPeriod:        200 * time.Millisecond,
			apiClient:          client,
			coreClient:         client.CoreV1(),
			coordinationClient: client.CoordinationV1(),
			logger:             &testLogger{t: t},
			errorLogger:        &testLogger{t: t},
			parentCtx:          context.Background(),
			ctx:                ctx,
			ctxCancel:          cancel,
			stopped:            make(chan struct{}),
		}
		leaderEngineMap[identity] = le

		var err error
		le.leaderElector, err = le.newLeaderElector(context.Background())
		if err != nil {
			t.Errorf("Had an error whilst creating a leader elector, err: %v", err)
		}
	}

	for identity, le := range leaderEngineMap {
		identity := identity
		le := le
		stopChannel := channelMap[identity]

		go testRunLeaderEngine(t, identity, le, stopChannel)
	}

	time.Sleep(500 * time.Millisecond)
	finishChannel := make(chan struct{}, 1)

	go func() {
		isFinished := map[string]bool{}
		count := 0
		for count < number {
			var currentLeader string
			for j := 0; j < number; j++ {
				identity := fmt.Sprintf("%s%d", holderIdentity, j)
				if isFinished[identity] {
					continue
				}

				le := leaderEngineMap[identity]
				if le.IsLeader() {
					currentLeader = identity
					break
				}
			}

			if currentLeader == "" {
				time.Sleep(10 * time.Millisecond) // Sleep some time to give time for reelection
				continue
			}

			t.Logf("Found leader, leader is %q, now stopping it", currentLeader)
			time.Sleep(50 * time.Millisecond)
			stopChannel := channelMap[currentLeader]
			stopChannel <- struct{}{}
			time.Sleep(50 * time.Millisecond)
			isFinished[currentLeader] = true
			count++
			t.Logf("Current count is %d, %d is remaining", count, number-count)
		}

		finishChannel <- struct{}{}
	}()

	select {
	case <-finishChannel:
		break
	case <-time.After(15 * time.Second):
		t.Errorf("Timed out after 15 seconds, possible deadlock")
	}
}

func testRunLeaderEngine(t *testing.T, identity string, le *K8sLeaderEngine, stopChannel chan struct{}) {
	rand.Seed(time.Now().UTC().UnixNano())

	randNum := (rand.Int() % 500)
	randInterval := time.Duration(randNum * int(time.Millisecond))
	time.Sleep(randInterval)

	if err := le.Start(); err != nil {
		t.Errorf("Failed to start leader election process for identity %q, err: %v", identity, err)
		return
	}

	<-stopChannel // wait for this to be signalled to be stopped

	if err := le.Stop(); err != nil {
		t.Errorf("Failed to stop leader election for identity %q, err: %v", identity, err)
		return
	}
	t.Logf("Stopped client with identity %q", identity)
}
