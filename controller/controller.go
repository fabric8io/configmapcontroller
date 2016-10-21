package controller

import (
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/pkg/errors"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/cache"
	"k8s.io/kubernetes/pkg/client/record"
	"k8s.io/kubernetes/pkg/client/restclient"
	client "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/controller/framework"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/watch"
)

const (
	updateOnChangeAnnotation = "metadata.annotations.configmap.fabric8.io/update-on-change"
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
				err := rollingUpgradeDeployments(cm, kubeClient)
				if err != nil {
					glog.Errorf("failed to update deployment: %v", err)
				}
			},
			UpdateFunc: func(oldObj interface{}, newObj interface{}) {
				cm := newObj.(*api.ConfigMap)
				err := rollingUpgradeDeployments(cm, kubeClient)
				if err != nil {
					glog.Errorf("failed to update deployment: %v", err)
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

func rollingUpgradeDeployments(cm *api.ConfigMap, c *client.Client) error {

	ns := cm.Namespace
	configMapVersion := cm.ResourceVersion

	deployments, err := c.Deployments(ns).List(api.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to list deployments")
	}
	for _, d := range deployments.Items {
		// match deployments with the correct annotation
		value, found := d.ObjectMeta.Annotations[updateOnChangeAnnotation]
		if found {
			// we can have multiple configmaps to update
			update := false
			configmaps := strings.Split(value, ",")
			for _, cm := range configmaps {
				containers := d.Spec.Template.Spec.Containers
				for _, container := range containers {
					envs := container.Env
					for _, e := range envs {
						match := "FABRIC8_" + strings.ToUpper(cm) + "_CONFIGMAP"
						if e.Name == match {
							e.Value = configMapVersion
							update = true
						}
					}
				}
			}
			// update the deployment if we've matched any configmaps
			if update {
				_, err := c.Deployments(ns).Update(&d)
				if err != nil {
					return errors.Wrap(err, "update deployment failed")
				}
			}
		}
	}
	return nil
}
