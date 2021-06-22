// +build windows

package leaderelection

const (
	ServiceAccountMountPath    = `C:\var\run\secrets\kubernetes.io\serviceaccount`
	DefaultKubernetesAPIServer = "https://kubernetes.default.svc.cluster.local"
)
