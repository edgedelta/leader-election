
![Build](https://github.com/edgedelta/leader-election/actions/workflows/go.yml/badge.svg)


## Kubernetes Leader Election Library for Go

This library provides a thin wrapper for kubernetes leader election. It can be used to elect a leader between multiple replica deployments/daemonsets. Behind the scenes kubernetes lease objects are used to persist leader information.

```
go get github.com/edgedelta/leader-election
```


### Example usage

```go

import (
    ...

    "github.com/edgedelta/leader-election"
)

func main() {
  le, err := leaderelection.New(
    leaderelection.WithLeaseDuration(15*time.Second),
    leaderelection.WithRenewTime(10*time.Second),
    leaderelection.WithRetryPeriod(2*time.Second),
    leaderelection.WithLeaseNamespace("custom-ns"))

  if err != nil {
    // Handle error
  }

  if err := le.Start(); err != nil {
    // Handle error
  }

  // run the leader election based logic
  ctx, cancel := context.WithCancel(context.Background())
  go run(le, ctx)

  // wait for termination signal
  termSignal := make(chan os.Signal, 1)
  signal.Notify(termSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
  <-termSignal

  // stop leader election engine
  cancel()
  if err := le.Stop(); err != nil {
    // Handle error
  }
}

func run(le *leaderelection.K8sLeaderEngine, ctx context.Context) {
  // do leader stuff as long as le.IsLeader() and ctx is not Done
}
```

### Check the lease holder
```
kubectl get lease -n custom-ns
```
