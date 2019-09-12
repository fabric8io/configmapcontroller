package controller

import (
	"bytes"
	"strings"
	"time"

	"github.com/fabric8io/configmapcontroller/util"
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

	"crypto/sha256"
	"encoding/base64"
	"sort"

	oclient "github.com/openshift/origin/pkg/client"
	deployapi "github.com/openshift/origin/pkg/deploy/api"
	deployapiv1 "github.com/openshift/origin/pkg/deploy/api/v1"
)

const (
	updateOnChangeAnnotation = "configmap.fabric8.io/update-on-change"
	separator                = ","
)

type Controller struct {
	client *client.Client

	cmController *framework.Controller
	cmLister     cache.StoreToServiceLister
	recorder     record.EventRecorder

	stopCh chan struct{}
}

func NewController(
	kubeClient *client.Client, ocClient *oclient.Client,
	restClientConfig *restclient.Config,
	encoder runtime.Encoder,
	resyncPeriod time.Duration, namespace string) (*Controller, error) {

	deployapi.AddToScheme(api.Scheme)
	deployapiv1.AddToScheme(api.Scheme)

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
				newCM := obj.(*api.ConfigMap)
				typeOfMaster, err := util.TypeOfMaster(kubeClient)
				if err != nil {
					glog.Fatalf("failed to create REST client config: %v", err)
				}
				switch typeOfMaster {
				case util.OpenShift:
					err := rollingUpgradeDeploymentsConfigs(newCM, ocClient)
					if err != nil {
						glog.Errorf("failed to update DeploymentConfig: %v", err)
					}
				case util.Kubernetes:
					err := rollingUpgradeDeployments(newCM, kubeClient)
					if err != nil {
						glog.Errorf("failed to update Deployment: %v", err)
					}
				}
			},
			UpdateFunc: func(oldObj interface{}, newObj interface{}) {
				oldM := oldObj.(*api.ConfigMap)
				newCM := newObj.(*api.ConfigMap)
				if oldM.ResourceVersion == newCM.ResourceVersion {
					return
				}

				typeOfMaster, err := util.TypeOfMaster(kubeClient)
				if err != nil {
					glog.Fatalf("failed to create REST client config: %v", err)
				}
				switch typeOfMaster {
				case util.OpenShift:
					err = rollingUpgradeDeploymentsConfigs(newCM, ocClient)
					if err != nil {
						glog.Errorf("failed to update DeploymentConfig: %v", err)
					}
				case util.Kubernetes:
					err = rollingUpgradeDeployments(newCM, kubeClient)
					if err != nil {
						glog.Errorf("failed to update Deployment: %v", err)
					}
				}
			},
		},
	)
	return &c, nil
}

// Run starts the controller.
func (c *Controller) Run() {
	glog.Info("starting configmapcontroller")

	go c.cmController.Run(c.stopCh)

	<-c.stopCh
}

func (c *Controller) Stop() {
	glog.Info("stopping configmapcontroller")

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
	configMapName := cm.Name
	configMapVersion := convertConfigMapToToken(cm)

	deployments, err := c.Deployments(ns).List(api.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to list deployments")
	}
	for _, d := range deployments.Items {
		containers := d.Spec.Template.Spec.Containers
		// match deployments with the correct annotation
		annotationValue := d.ObjectMeta.Annotations[updateOnChangeAnnotation]
		if annotationValue != "" {
			values := strings.Split(annotationValue, separator)
			matches := false
			for _, value := range values {
				if value == configMapName {
					matches = true
					break
				}
			}
			if matches {
				updateContainers(containers, annotationValue, configMapVersion)

				// update the deployment
				_, err := c.Deployments(ns).Update(&d)
				if err != nil {
					return errors.Wrap(err, "update deployment failed")
				}
				glog.Infof("Updated Deployment %s", d.Name)
			}
		}
	}
	return nil
}

func rollingUpgradeDeploymentsConfigs(cm *api.ConfigMap, oc *oclient.Client) error {
	ns := cm.Namespace
	configMapName := cm.Name
	configMapVersion := convertConfigMapToToken(cm)
	dcs, err := oc.DeploymentConfigs(ns).List(api.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "failed to list deploymentsconfigs")
	}

	//glog.Infof("found %v DC items in namespace %s", len(dcs.Items), ns)
	for _, d := range dcs.Items {
		containers := d.Spec.Template.Spec.Containers
		// match deployment configs with the correct annotation
		annotationValue := d.ObjectMeta.Annotations[updateOnChangeAnnotation]
		if annotationValue != "" {
			values := strings.Split(annotationValue, separator)
			matches := false
			for _, value := range values {
				if value == configMapName {
					matches = true
					break
				}
			}
			if matches {
				if updateContainers(containers, annotationValue, configMapVersion) {
					// update the deployment
					_, err := oc.DeploymentConfigs(ns).Update(&d)
					if err != nil {
						return errors.Wrap(err, "update deployment failed")
					}
					glog.Infof("Updated DeploymentConfigs %s", d.Name)
				}
			}
		}
	}
	return nil
}

// convertConfigMapToToken converts the configmap into a unique token based on the data values
func convertConfigMapToToken(cm *api.ConfigMap) string {
	values := []string{}
	for k, v := range cm.Data {
		values = append(values, k+"="+v)
	}
	sort.Strings(values)
	text := strings.Join(values, ";")
	hasher := sha256.New()
	if _, err := hasher.Write([]byte(text)); err != nil {
		glog.Error(err)
	}
	sha := base64.URLEncoding.EncodeToString(hasher.Sum(nil))
	return sha
}

// updateContainers returns a boolean value indicating if any containers have been updated
func updateContainers(containers []api.Container, annotationValue, configMapVersion string) bool {
	// we can have multiple configmaps to update
	updated := false
	configmaps := strings.Split(annotationValue, separator)
	for _, cmNameToUpdate := range configmaps {
		configmapEnvar := "FABRIC8_" + convertToEnvVarName(cmNameToUpdate) + "_CONFIGMAP"

		for i := range containers {
			envs := containers[i].Env
			matched := false
			for j := range envs {
				if envs[j].Name == configmapEnvar {
					matched = true
					if envs[j].Value != configMapVersion {
						glog.Infof("Updating %s to %s", configmapEnvar, configMapVersion)
						envs[j].Value = configMapVersion
						updated = true
					}
				}
			}
			// if no existing env var exists lets create one
			if !matched {
				e := api.EnvVar{
					Name:  configmapEnvar,
					Value: configMapVersion,
				}
				containers[i].Env = append(containers[i].Env, e)
				updated = true
			}
		}
	}
	return updated
}

// convertToEnvVarName converts the given text into a usable env var
// removing any special chars with '_'
func convertToEnvVarName(text string) string {
	var buffer bytes.Buffer
	upper := strings.ToUpper(text)
	lastCharValid := false
	for i := 0; i < len(upper); i++ {
		ch := upper[i]
		if ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' {
			buffer.WriteByte(ch)
			lastCharValid = true
			continue
		}
		if lastCharValid {
			buffer.WriteByte('_')
		}
		lastCharValid = false
	}
	return buffer.String()
}
