package pkg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"

	api "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/klog"
)

type KubernetesClient struct {
	clientset *kubernetes.Clientset

	restConfig       *rest.Config
	kubernetesConfig string
}

func NewKubernetesClient(kubeconfig string) (*KubernetesClient, error) {
	klog.Infof(kubeconfig)
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}

	restConfig, err := NewRestConfig(kubeconfig)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &KubernetesClient{
		clientset:        clientset,
		restConfig:       restConfig,
		kubernetesConfig: kubeconfig,
	}, nil
}

func NewRestConfig(kubeConfig string) (*rest.Config, error) {
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{
			ExplicitPath: kubeConfig,
		},
		&clientcmd.ConfigOverrides{},
	)
	clusterConfig, err := clientConfig.ClientConfig()
	if err != nil {
		klog.Warningf("create new k8s rest client for %s got error:%s", kubeConfig, err)
		return nil, err
	}

	//clusterConfig.ContentConfig.GroupVersion = &v1alpha1.SchemeGroupVersion
	clusterConfig.APIPath = "/apis"
	//clusterConfig.NegotiatedSerializer = serializer.DirectCodecFactory{CodecFactory: scheme.Codecs}
	clusterConfig.UserAgent = rest.DefaultKubernetesUserAgent()
	return clusterConfig, nil
}

type Option struct {
	NameSpace string
	PodName   string
	Container string
	Commands  []string
}

type Response struct {
	Code    int32       `json:"code"`
	Success bool        `json:"success"`
	Err     string      `json:"error,omitempty"`
	Result  interface{} `json:"result,omitempty"`
}

func (c *KubernetesClient) ExecuteCommand(opt *Option, isDestroy bool) *Response {
	klog.Infof("Start to execute command :%s", opt)
	if len(opt.PodName) == 0 {
		klog.Errorf("can not execute command with empty pod name: %s", opt)
		return &Response{
			Code: -1, Success: false, Err: "can not execute command with empty pod name",
		}
	}

	if len(opt.NameSpace) == 0 {
		opt.NameSpace = "default"
	}

	pod, err := c.clientset.CoreV1().Pods(opt.NameSpace).Get(context.TODO(), opt.PodName, v1.GetOptions{})
	if err != nil {
		klog.Errorf("get pod for %s got error : %s", opt, err)
		notFoundInfo := fmt.Sprintf("pods \"%s\" not found", opt.PodName)
		if isDestroy && err.Error() == notFoundInfo {
			return &Response{
				Code: 1, Success: true, Err: "",
			}
		}

		return &Response{
			Code: -1, Success: false, Err: err.Error(),
		}
	}

	if pod.Status.Phase == api.PodSucceeded || pod.Status.Phase == api.PodFailed {
		return &Response{
			Code: -1, Success: false, Err: fmt.Sprintf("cannot exec into a container in a completed pod; current phase is %s", pod.Status.Phase),
		}
	}

	if len(opt.Container) == 0 {
		if len(pod.Spec.Containers) > 1 {
			klog.Warningf("Defaulting container name to %s.", pod.Spec.Containers[0].Name)
		}
		opt.Container = pod.Spec.Containers[0].Name
	} else {
		matched := false
		for _, c := range pod.Spec.Containers {
			if c.Name == opt.Container {
				matched = true
				break
			}
		}

		if !matched {
			return &Response{
				Code: -1, Success: false, Err: fmt.Sprintf("container name: %s not found in pod %s", opt.Container, opt.PodName),
			}
		}
	}

	restClient := c.clientset.CoreV1().RESTClient()
	req := restClient.Post().
		Resource("pods").
		Name(opt.PodName).
		Namespace(opt.NameSpace).
		SubResource("exec")

	req.VersionedParams(&api.PodExecOptions{
		Container: opt.Container,
		Command:   opt.Commands,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
		TTY:       false,
	}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(c.restConfig, "POST", req.URL())
	if err != nil {
		klog.Errorf("error when NewSPDYExecutor, err: %s", err)
		return &Response{
			Code: -1, Success: false, Err: fmt.Sprintf("error when NewSPDYExecutor, err: %s", err),
		}
	}

	reader, writer := io.Pipe()
	go func() {
		defer writer.Close()
		err = exec.Stream(remotecommand.StreamOptions{
			Stdout: writer,
			Stderr: writer,
			Tty:    false,
		})
	}()

	buffer, err := ioutil.ReadAll(reader)
	if err != nil {
		klog.Warningf("read resp got error: %s", err)
		return &Response{
			Code: -1, Success: false, Err: fmt.Sprintf("read resp got error: %s", err),
		}
	}

	respString := string(buffer)
	klog.Infof("exec result for :%s: ret: %s", opt, respString)
	var resp Response
	err = json.Unmarshal(buffer, &resp)
	if err != nil {
		klog.Warningf("unmarsh json %s got error: %s", respString, err)
		return &Response{
			Code: -1, Success: false, Err: fmt.Sprintf("unmarsh json %s got error: %s", respString, err),
		}
	}

	return &resp
}
