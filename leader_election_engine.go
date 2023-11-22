package leaderelection

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"

	coordinationV1 "k8s.io/api/coordination/v1"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedCoreV1 "k8s.io/client-go/kubernetes/typed/core/v1"
	ld "k8s.io/client-go/tools/leaderelection"
	rl "k8s.io/client-go/tools/leaderelection/resourcelock"
)

func (le *K8sLeaderEngine) newLeaderElector(ctx context.Context) (*ld.LeaderElector, error) {
	// Double check the lease is already created
	if err := le.ensureLeaseCreated(ctx); err != nil {
		return nil, err
	}

	currentLeader, lease, err := le.getCurrentLeader(ctx)
	if err != nil {
		return nil, err
	}
	le.logger.Log("Current leader is %q and new elector will be generated as %q will be the new candidate", currentLeader, le.holderIdentity)

	callbacks := ld.LeaderCallbacks{
		OnNewLeader: func(identity string) {
			le.updateLeaderIdentity(identity)
			le.logger.Log("New leader selected for lease name: %q and namespace: %q, new leader is %q", le.leaseName, le.leaseNamespace, identity)
		},
		OnStartedLeading: func(ctx context.Context) {
			le.updateLeaderIdentity(le.holderIdentity)
			le.logger.Log("Started leading as %q", le.holderIdentity)
		},
		OnStoppedLeading: func() {
			le.updateLeaderIdentity("")
			le.logger.Log("Stopped leading as %q", le.holderIdentity)
		},
	}

	eventSource := coreV1.EventSource{
		Component: "leader-elector",
		Host:      le.holderIdentity,
	}
	broadcaster := record.NewBroadcaster()
	broadcaster.StartLogging(le.logger.Log)
	broadcaster.StartRecordingToSink(&typedCoreV1.EventSinkImpl{
		Interface: le.coreClient.Events(le.leaseNamespace),
	})

	eventRecorder := broadcaster.NewRecorder(scheme.Scheme, eventSource)
	resourceLockConfig := rl.ResourceLockConfig{
		Identity:      le.holderIdentity,
		EventRecorder: eventRecorder,
	}

	leaderElectorLock, err := rl.New(
		rl.LeasesResourceLock,
		lease.ObjectMeta.Namespace,
		lease.ObjectMeta.Name,
		nil,
		le.coordinationClient,
		resourceLockConfig,
	)
	if err != nil {
		return nil, err
	}

	leaderElectionConfig := ld.LeaderElectionConfig{
		Lock:          leaderElectorLock,
		LeaseDuration: le.leaseDuration,
		RenewDeadline: le.renewDeadline,
		RetryPeriod:   le.retryPeriod,
		Callbacks:     callbacks,
	}
	return ld.NewLeaderElector(leaderElectionConfig)
}

func (le *K8sLeaderEngine) ensureLeaseCreated(ctx context.Context) error {
	_, err := le.coordinationClient.Leases(le.leaseNamespace).Get(ctx, le.leaseName, metav1.GetOptions{})
	if err == nil {
		return nil
	}

	if !errors.IsNotFound(err) {
		return err
	}

	// create lease if no exists already
	_, err = le.coordinationClient.Leases(le.leaseNamespace).Create(ctx, &coordinationV1.Lease{
		TypeMeta: metav1.TypeMeta{
			Kind: "Lease",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: le.leaseName,
		},
	}, metav1.CreateOptions{})

	if err == nil {
		return nil
	}

	if !errors.IsConflict(err) || errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create lease: %s, err: %v", le.leaseName, err)
	}

	return nil
}

func (le *K8sLeaderEngine) getCurrentLeader(ctx context.Context) (string, *coordinationV1.Lease, error) {
	lease, err := le.coordinationClient.Leases(le.leaseNamespace).Get(ctx, le.leaseName, metav1.GetOptions{})
	if err != nil {
		return "", nil, err
	}

	value, ok := lease.Annotations[rl.LeaderElectionRecordAnnotationKey]
	if !ok {
		le.logger.Log("The lease has no annotation with respect to leader selection, no one is leading at the namespace: %q with respect to key: %q", le.leaseNamespace, rl.LeaderElectionRecordAnnotationKey)
		return "", lease, nil
	}

	electionRecord := rl.LeaderElectionRecord{}
	if err := json.Unmarshal([]byte(value), &electionRecord); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal election record, err: %v", err)
	}

	return electionRecord.HolderIdentity, lease, nil
}

func (le *K8sLeaderEngine) updateLeaderIdentity(leaderIdentity string) {
	le.leaderIdentityMutex.Lock()
	previousLeader := le.currentLeaderIdentity
	le.currentLeaderIdentity = leaderIdentity
	le.leaderIdentityMutex.Unlock()

	payload := &NewLeaderPayload{
		CurrentlyLeading: le.IsLeader(),
		OldLeader:        previousLeader,
		NewLeader:        leaderIdentity,
	}

	le.subscribersMu.Lock()
	defer le.subscribersMu.Unlock()
	for name, subscriber := range le.subscribers {
		name := name
		subscriber := subscriber
		go func() {
			if err := subscriber.NewLeader(payload); err != nil {
				le.errorLogger.Log("Failed to send new leader event to subscriber: %q, err: %v", name, err)
			}
		}()
	}
}
