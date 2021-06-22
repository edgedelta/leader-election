// +build linux darwin

package leaderelection

const (
	ServiceAccountMountPath    = "/var/run/secrets/kubernetes.io/serviceaccount"
	DefaultKubernetesAPIServer = "https://kubernetes.default.svc"
)
