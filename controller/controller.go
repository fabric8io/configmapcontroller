package controller

import (
	"strings"
	"time"

	"github.com/golang/glog"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/cache"
	"k8s.io/kubernetes/pkg/client/record"
	"k8s.io/kubernetes/pkg/client/restclient"
	client "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/controller/framework"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/watch"
)

type Controller struct {
	client *client.Client

	cmController *framework.Controller
	cmLister     cache.StoreToServiceLister
	recorder     record.EventRecorder

	stopCh chan struct{}
}

func NewController(
	kubeClient *client.Client,
	restClientConfig *restclient.Config,
	encoder runtime.Encoder,
	resyncPeriod time.Duration, namespace string) (*Controller, error) {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(kubeClient.Events(namespace))

	c := Controller{
		client: kubeClient,
		stopCh: make(chan struct{}),
		recorder: eventBroadcaster.NewRecorder(api.EventSource{
			Component: "configmap-controller",
		}),
	}

	c.cmLister.Store, c.cmController = framework.NewInformer(
		&cache.ListWatch{
			ListFunc:  configMapListFunc(c.client, namespace),
			WatchFunc: configMapWatchFunc(c.client, namespace),
		},
		&api.ConfigMap{},
		resyncPeriod,
		framework.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				cm := obj.(*api.ConfigMap)
				err := configAdded(cm)
				if err != nil {
					glog.Errorf("Add failed: %v", err)
				}

			},
			UpdateFunc: func(oldObj interface{}, newObj interface{}) {
				cm := newObj.(*api.ConfigMap)
				err := configUpdated(cm)
				if err != nil {
					glog.Errorf("Add failed: %v", err)
				}

			},
			DeleteFunc: func(obj interface{}) {
				cm, ok := obj.(cache.DeletedFinalStateUnknown)
				if ok {
					// configmap key is in the form namespace/name
					split := strings.Split(cm.Key, "/")
					ns := split[0]
					name := split[1]
					err := configDeleted(ns, name)
					if err != nil {
						glog.Errorf("Remove failed: %v", err)
					}
				}
			},
		},
	)

	return &c, nil
}

// Run starts the controller.
func (c *Controller) Run() {
	glog.Infof("starting configmapcontroller")

	go c.cmController.Run(c.stopCh)

	<-c.stopCh
}

func (c *Controller) Stop() {
	glog.Infof("stopping configmapcontroller")

	close(c.stopCh)
}

func configMapListFunc(c *client.Client, ns string) func(api.ListOptions) (runtime.Object, error) {
	return func(opts api.ListOptions) (runtime.Object, error) {
		return c.ConfigMaps(ns).List(opts)
	}
}

func configMapWatchFunc(c *client.Client, ns string) func(options api.ListOptions) (watch.Interface, error) {
	return func(options api.ListOptions) (watch.Interface, error) {
		return c.ConfigMaps(ns).Watch(options)
	}
}

func configAdded(cm *api.ConfigMap) error {
	glog.Infof("configmap added %s", cm.Name)
	return nil
}

func configUpdated(cm *api.ConfigMap) error {
	glog.Infof("configmap updated %s", cm.Name)
	return nil
}

func configDeleted(ns, cm string) error {
	glog.Infof("configmap %s deleted in namespace %s", cm, ns)
	return nil
}
