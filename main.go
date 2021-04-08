package main

import (
	"github.com/wccsama/kubectl-exec-go/pkg"
	"k8s.io/klog"
)

func main() {
	kc, err := pkg.NewKubernetesClient("")
	if err != nil {
		klog.Errorf("NewKubernetesClient err: &v", err)
	}

	_ = kc.ExecuteCommand(&pkg.Option{}, false)
}
