## Kubernetes Leader Election Library for Go

This library provides a thin wrapper for kubernetes leader election. It can be used to elect a leader between multiple replica deployments/daemonsets. Behind the scenes kubernetes lease objects are used to persist leader information.

```
go get github.com/edgedelta/leader-election
```


### Example usage

```go
func main() {
  le, err := New(
    WithleaseDuration(15*time.Second),
    WithRenewTime(10*time.Second),
    WithRetryPeriod(2*time.Second),
    WithLeaseNamespace("custom-ns"))

  if err != nil {
    // Handle error
  }

  if err := le.Start(); err != nil {
    // Handle error
  }

  // run the leader election check
  ctx, cancel := context.WithCancel(context.Background())
  go run(le, ctx)

  // wait for termination signal
  termSignal := make(chan os.Signal, 1)
  signal.Notify(termSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
  <-termSignal

  // stop the leader election check
  cancel()

  if err := le.Stop(); err != nil {
    // Handle error
  }
}

func run(le *K8sLeaderEngine, ctx context.Context) {
  // do leader stuff as long as le.IsLeader() and ctx is not Done
}
```

### Check the lease holder
```
kubectl get lease -n custom-ns
```