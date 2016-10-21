package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fabric8io/configmapcontroller/client"
	"github.com/fabric8io/configmapcontroller/controller"
	"github.com/fabric8io/configmapcontroller/util"
	"github.com/fabric8io/configmapcontroller/version"
	"github.com/golang/glog"
	oclient "github.com/openshift/origin/pkg/client"
	"github.com/spf13/pflag"
	"k8s.io/kubernetes/pkg/api"
	kubectlutil "k8s.io/kubernetes/pkg/kubectl/cmd/util"
)

const (
	healthPort = 10254
)

var (
	flags = pflag.NewFlagSet("", pflag.ExitOnError)

	resyncPeriod = flags.Duration("sync-period", 30*time.Second,
		`Relist and confirm services this often.`)

	healthzPort = flags.Int("healthz-port", healthPort, "port for healthz endpoint.")

	profiling = flags.Bool("profiling", true, `Enable profiling via web interface host:port/debug/pprof/`)
)

func main() {
	factory := kubectlutil.NewFactory(nil)
	factory.BindFlags(flags)
	factory.BindExternalFlags(flags)
	flags.Parse(os.Args)
	flag.CommandLine.Parse([]string{})

	glog.Infof("Using build: %v", version.Version)

	kubeClient, err := factory.Client()
	if err != nil {
		glog.Fatalf("failed to create client: %s", err)
	}

	restClientConfig, err := factory.ClientConfig()
	if err != nil {
		glog.Fatalf("failed to create REST client config: %s", err)
	}

	var oc *oclient.Client
	typeOfMaster, err := util.TypeOfMaster(kubeClient)
	if err != nil {
		glog.Fatalf("failed to create REST client config: %s", err)
	}
	if typeOfMaster == util.OpenShift {
		oc, _, err = client.NewOpenShiftClient(restClientConfig)
		if err != nil {
			glog.Fatalf("failed to create REST client config: %s", err)
		}
	}

	c, err := controller.NewController(kubeClient, oc, restClientConfig, factory.JSONEncoder(), *resyncPeriod, api.NamespaceAll)
	if err != nil {
		glog.Fatalf("%s", err)
	}

	go registerHandlers()
	go handleSigterm(c)

	c.Run()
}

func registerHandlers() {
	mux := http.NewServeMux()

	if *profiling {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	}

	server := &http.Server{
		Addr:    fmt.Sprintf(":%v", *healthzPort),
		Handler: mux,
	}
	glog.Fatal(server.ListenAndServe())
}

func handleSigterm(c *controller.Controller) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-signalChan
	glog.Infof("Received %s, shutting down", sig)
	c.Stop()
}
