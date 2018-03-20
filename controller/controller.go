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

	"sort"

	oclient "github.com/openshift/origin/pkg/client"
	deployapi "github.com/openshift/origin/pkg/deploy/api"
	deployapiv1 "github.com/openshift/origin/pkg/deploy/api/v1"

	"fmt"
	"gopkg.in/v2/yaml"
	"io"
	"os/exec"
)

const (
	updateOnChangeAnnotation = "configmap.fabric8.io/update-on-change"
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

	cmController *framework.Controller
	cmLister     cache.StoreToServiceLister
	recorder     record.EventRecorder

	stopCh chan struct{}
}

func NewController(
	kubeClient *client.Client,
	ocClient *oclient.Client,
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

func rollingUpgradeObject(cm *api.ConfigMap, objectKind string) error {
	objects := findObjectsByKind(objectKind)
	configMapName := cm.Name
	configMapLabel := "FABRIC8_" + convertToEnvVarName(configMapName) + "_CONFIGMAP"

	for _, o := range objects.Items {
		annotationValue := o.Metadata.Annotations[updateOnChangeAnnotation]
		if annotationValue != "" {
			values := strings.Split(annotationValue, ",")
			for _, value := range values {
				if value == configMapName {
					go RunKubectlPatch(objectKind, o.Metadata.Name, configMapLabel, cm.ObjectMeta.ResourceVersion)
				}
			}
		}
	}
	return nil
}

// lets convert the configmap into a unique token based on the data values
func convertConfigMapToToken(cm *api.ConfigMap) string {
	values := []string{}
	for k, v := range cm.Data {
		values = append(values, k+"="+v)
	}
	sort.Strings(values)
	text := strings.Join(values, ";")
	// we could zip and base64 encode
	// but for now we could leave this easy to read so that its easier to diagnose when & why things changed
	return text
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
	attemptDelay := 30
	for attempt := 1; attempt < 3; attempt++ {
		err := exec.Command("/kubectl", "patch", objectKind, objectId, "--patch", yamlPatch).Run()
		if err == nil {
			glog.Infof("Successfully sent patch request for %s %s", objectKind, objectId)
			return
		}
		glog.Errorf("error running kubectl attempt %d. Waiting %d seconds", attempt, attemptDelay)
		fmt.Printf("%s", errors.Wrap(err, "error patching element"))
		time.Sleep(time.Duration(attemptDelay) * time.Second)
		attemptDelay = attemptDelay * 2
	}
	glog.Errorf("Could not execute patch %s on object %s of kind %s", yamlPatch, objectId, objectKind)
}
