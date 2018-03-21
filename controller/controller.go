package controller

import (
	"bytes"
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

	deployapi "github.com/openshift/origin/pkg/deploy/api"
	deployapiv1 "github.com/openshift/origin/pkg/deploy/api/v1"

	"fmt"
	"gopkg.in/v2/yaml"
	"io"
	"os/exec"
)

const (
	updateOnChangeAnnotationSuffix = ".fabric8.io/update-on-change"
)

type AnnotationsField map[string]string
type MetadataField struct {
	Name        string
	Annotations AnnotationsField
}
type GenericAnnotatedObject struct {
	Kind     string
	Metadata MetadataField
}
type ObjectList struct {
	Items []GenericAnnotatedObject
}
type Controller struct {
	client *client.Client

	cmController     *framework.Controller
	cmLister         cache.StoreToServiceLister
	secretController *framework.Controller
	secretLister     cache.StoreToServiceLister
	recorder         record.EventRecorder

	stopCh chan struct{}
}

func NewController(
	kubeClient *client.Client,
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
				cm := obj.(*api.ConfigMap)
				go rollingUpgradeObject(cm, "Deployment")
				go rollingUpgradeObject(cm, "DaemonSet")
				go rollingUpgradeObject(cm, "StatefulSet")
			},
			UpdateFunc: func(oldObj interface{}, newObj interface{}) {
				oldM := oldObj.(*api.ConfigMap)
				newCM := newObj.(*api.ConfigMap)

				if oldM.ResourceVersion != newCM.ResourceVersion {
					go rollingUpgradeObject(newCM, "Deployment")
					go rollingUpgradeObject(newCM, "DaemonSet")
					go rollingUpgradeObject(newCM, "StatefulSet")
				}
			},
		},
	)
	c.secretLister.Store, c.secretController = framework.NewInformer(
		&cache.ListWatch{
			ListFunc:  secretListFunc(c.client, namespace),
			WatchFunc: secretWatchFunc(c.client, namespace),
		},
		&api.Secret{},
		resyncPeriod,
		framework.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				s := obj.(*api.Secret)
				go rollingUpgradeObject(s, "Deployment")
				go rollingUpgradeObject(s, "DaemonSet")
				go rollingUpgradeObject(s, "StatefulSet")
			},
			UpdateFunc: func(oldObj interface{}, newObj interface{}) {
				oldSec := oldObj.(*api.Secret)
				newSec := newObj.(*api.Secret)

				if oldSec.ResourceVersion != newSec.ResourceVersion {
					go rollingUpgradeObject(newSec, "Deployment")
					go rollingUpgradeObject(newSec, "DaemonSet")
					go rollingUpgradeObject(newSec, "StatefulSet")
				}
			},
		},
	)
	return &c, nil
}

// Run starts the controller.
func (c *Controller) Run() {
	glog.Infof("Starting configmapcontroller. Watching configmaps")
	go c.cmController.Run(c.stopCh)
	glog.Infof("Starting configmapcontroller. Watching secrets")
	go c.secretController.Run(c.stopCh)
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

func secretListFunc(c *client.Client, ns string) func(api.ListOptions) (runtime.Object, error) {
	glog.Infof("Listing secrets")
	return func(opts api.ListOptions) (runtime.Object, error) {
		return c.Secrets(ns).List(opts)
	}
}

func secretWatchFunc(c *client.Client, ns string) func(options api.ListOptions) (watch.Interface, error) {
	glog.Infof("Watching secrets")
	return func(options api.ListOptions) (watch.Interface, error) {
		return c.Secrets(ns).Watch(options)
	}
}

func findObjectsByKind(objectKind string) ObjectList {
	kubectlbinary := "/kubectl"
	cmd := exec.Command(kubectlbinary, "get", objectKind, "-o", "yaml")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		glog.Errorf("Error retrieving objects by kind. Could not start pipe.")
		var emptyObjectList ObjectList
		return emptyObjectList
	}
	if err := cmd.Start(); err != nil {
		glog.Errorf("Error retrieving objects by kind. Could not query.")
		var emptyObjectList ObjectList
		return emptyObjectList
	}
	buf := bytes.NewBuffer(nil)
	io.Copy(buf, stdout)
	var g ObjectList
	yaml.Unmarshal(buf.Bytes(), &g)
	return g
}

func rollingUpgradeObject(objectThatChanged interface{}, objectKind string) {
	glog.Infof("Rolling upgrade object %s", objectKind)
	nameOfObjectThatChanged, typeOfObjectThatChanged, versionOfObjectThatChanged := getObjectVars(objectThatChanged)
	glog.Infof("Object that changed: %s %s %s", nameOfObjectThatChanged, typeOfObjectThatChanged, versionOfObjectThatChanged)
	if typeOfObjectThatChanged == "" {
		glog.Infof("Type of object that changed is not handled. Type: [%s]", typeOfObjectThatChanged)
		return
	}
	updateOnChangeAnnotation := strings.ToLower(typeOfObjectThatChanged) + updateOnChangeAnnotationSuffix
	labelName := "FABRIC8_" + convertToEnvVarName(nameOfObjectThatChanged) + "_" + strings.ToUpper(typeOfObjectThatChanged)
	glog.Infof("Label: %s", labelName)
	glog.Infof("Annotation: %s", updateOnChangeAnnotation)
	objects := findObjectsByKind(objectKind)
	for _, o := range objects.Items {
		annotationValue := o.Metadata.Annotations[updateOnChangeAnnotation]
		glog.Infof("Object: %s, Annotation value: %s", o.Metadata.Name, annotationValue)
		if annotationValue != "" {
			values := strings.Split(annotationValue, ",")
			for _, value := range values {
				if value == nameOfObjectThatChanged {
					go RunKubectlPatch(objectKind, o.Metadata.Name, labelName, versionOfObjectThatChanged)
				}
			}
		}
	}
}

func getObjectVars(o interface{}) (string, string, string) {
	glog.Infof("obtaining object variables")
	cm, ok := o.(*api.ConfigMap)
	if ok {
		glog.Infof("returning variables for configmap")
		return cm.Name, "CONFIGMAP", cm.ObjectMeta.ResourceVersion
	}
	sec, ok := o.(*api.Secret)
	if ok {
		glog.Infof("returning variables for secret")
		return sec.Name, "SECRET", sec.ObjectMeta.ResourceVersion
	}
	glog.Errorf("The reported object that changed is not a Secret or a ConfigMap")
	return "", "", ""
}

// convertToEnvVarName converts the given text into a usable env var
// removing any special chars with '_'
func convertToEnvVarName(text string) string {
	var buffer bytes.Buffer
	lower := strings.ToUpper(text)
	lastCharValid := false
	for i := 0; i < len(lower); i++ {
		ch := lower[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			buffer.WriteString(string(ch))
			lastCharValid = true
		} else {
			if lastCharValid {
				buffer.WriteString("_")
			}
			lastCharValid = false
		}
	}
	return buffer.String()
}

func RunKubectlPatch(objectKind string, objectId string, labelName string, labelValue string) {
	yamlPatch := fmt.Sprintf("spec:\n  template:\n    metadata:\n      labels:\n        %s: '%s'", labelName, labelValue)
	glog.Infof("About to run patch: %s %s with %s", objectKind, objectId, yamlPatch)
	err := exec.Command("/kubectl", "patch", objectKind, objectId, "--patch", yamlPatch).Run()
	if err == nil {
		glog.Infof("Successfully sent patch request for %s %s", objectKind, objectId)
		return
	}
	glog.Errorf("Could not execute patch %s on object %s of kind %s", yamlPatch, objectId, objectKind)
}
