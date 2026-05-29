package clients

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/pkg/errors"
	versioned2 "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"go.uber.org/zap"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	// most programming languages  do not have a timeout, but c# does a default
	// of 100 seconds so using that value.
	ConnectMaxWaitTime = 100 * time.Second
	RequestMaxWaitTime = 100 * time.Second

	DefaultRemoteResourceMaxResponseBytes int64 = 1024 * 1024
)

type Clients struct {
	ClientInitialized bool
	PipelineAsCode    versioned.Interface
	Tekton            versioned2.Interface
	Kube              kubernetes.Interface
	HTTP              http.Client
	Log               *zap.SugaredLogger
	Dynamic           dynamic.Interface
	consoleUIMutex    *sync.Mutex
	consoleUI         consoleui.Interface
}

func (c *Clients) InitClients() {
	c.consoleUIMutex = &sync.Mutex{}
}

func (c *Clients) GetURL(ctx context.Context, url string) ([]byte, error) {
	nctx, cancel := context.WithTimeout(ctx, RequestMaxWaitTime)
	defer cancel()

	req, err := http.NewRequestWithContext(nctx, http.MethodGet, url, nil)
	if err != nil {
		return []byte{}, err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return []byte{}, err
	}
	defer res.Body.Close()
	statusOK := res.StatusCode >= 200 && res.StatusCode < 300
	if !statusOK {
		return nil, fmt.Errorf("Non-OK HTTP status: %d", res.StatusCode)
	}

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return []byte{}, err
	}
	return data, nil
}

// Set kube client based on config.
func (c *Clients) kubeClient(config *rest.Config) (kubernetes.Interface, error) {
	k8scs, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create k8s client from config")
	}

	return k8scs, nil
}

func (c *Clients) dynamicClient(config *rest.Config) (dynamic.Interface, error) {
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create dynamic client from config")
	}
	return dynamicClient, err
}

func (c *Clients) kubeConfig(info *info.Info) (*rest.Config, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if info.Kube.ConfigPath != "" {
		loadingRules.ExplicitPath = info.Kube.ConfigPath
	}
	configOverrides := &clientcmd.ConfigOverrides{}
	if info.Kube.Context != "" {
		configOverrides.CurrentContext = info.Kube.Context
	}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
	if info.Kube.Namespace == "" {
		namespace, _, err := kubeConfig.Namespace()
		if err != nil {
			return nil, errors.Wrap(err, "Couldn't get kubeConfiguration namespace")
		}
		info.Kube.Namespace = namespace
	}
	config, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, errors.Wrap(err, "Parsing kubeconfig failed")
	}
	return config, nil
}

func (c *Clients) tektonClient(config *rest.Config) (versioned2.Interface, error) {
	cs, err := versioned2.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return cs, nil
}

func (c *Clients) pacClient(config *rest.Config) (versioned.Interface, error) {
	cs, err := versioned.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return cs, nil
}

func (c *Clients) consoleUIClient(ctx context.Context, dynamic dynamic.Interface, info *info.Info) consoleui.Interface {
	return consoleui.New(ctx, dynamic, info)
}

func (c *Clients) NewClients(ctx context.Context, info *info.Info) error {
	if c.consoleUIMutex == nil {
		c.consoleUIMutex = &sync.Mutex{}
	}
	if c.ClientInitialized {
		return nil
	}
	prod, _ := zap.NewProduction()
	logger := prod.Sugar()
	defer func() {
		_ = logger.Sync() // flushes buffer, if any
	}()
	c.Log = logger

	c.HTTP = http.Client{
		Timeout: RequestMaxWaitTime,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: ConnectMaxWaitTime,
			}).DialContext,
		},
	}
	config, err := c.kubeConfig(info)
	if err != nil {
		return err
	}
	config.QPS = 50
	config.Burst = 50

	c.Kube, err = c.kubeClient(config)
	if err != nil {
		return err
	}
	c.Tekton, err = c.tektonClient(config)
	if err != nil {
		return err
	}

	c.PipelineAsCode, err = c.pacClient(config)
	if err != nil {
		return err
	}

	c.Dynamic, err = c.dynamicClient(config)
	if err != nil {
		return err
	}

	c.SetConsoleUI(c.consoleUIClient(ctx, c.Dynamic, info))
	c.ClientInitialized = true

	return nil
}

func (c *Clients) ConsoleUI() consoleui.Interface {
	c.consoleUIMutex.Lock()
	defer c.consoleUIMutex.Unlock()
	return c.consoleUI
}

func (c *Clients) SetConsoleUI(consoleUI consoleui.Interface) {
	if c.consoleUIMutex == nil {
		c.consoleUIMutex = &sync.Mutex{}
	}
	c.consoleUIMutex.Lock()
	defer c.consoleUIMutex.Unlock()
	c.consoleUI = consoleUI
}
