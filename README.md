# exposecontroller

Automatically expose services creating ingress rules, openshift routes or modifying services to use kubernetes nodePort or loadBalancer service types

## Getting started

We're adding support for Helm however until then if you want to get going quick using our defaults:

```sh
kubectl create -f http://central.maven.org/maven2/io/fabric8/devops/apps/exposecontroller/2.2.268/exposecontroller-2.2.268-kubernetes.yml
```

```sh
kubectl label svc foo expose=true
```

If you have [gofabric8](https://github.com/fabric8io/gofabric8) then
```sh
gofabric8 service foo
```

else get the external URL from the service annotation and paste in into your browser
```sh
kubectl get svc foo -o=yaml
```
## Configuration

We use a Kubernetes ConfigMap and two main config entries
  - `domain` when using either Kubernetes Ingress or OpenShift Routes you will need to set the domain that you've used with your DNS provider (fabric8 uses [cloudflare](https://www.cloudflare.com)) or xip.io if you want a quick way to get running.
  - `exposer` used to describe which strategy exposecontroller should use to access applications

### Automatic

If no config map or data values provided exposecontroller will try and work out what `exposer` or `domain` config for the playform.  

* exposer - Minishift anf Minikube will default to `NodePort`, we  use `Ingress` for Kubernetes or `Route` OpenShift.
* domain - using [xip.io](http://xip.io/) for magic wildcard DNS, exposecontroller will try and find a https://stackpoint.io HAProxy or Nginx Ingress controller.  We also default to the single VM IP if using minishift or minikube.  Together these create an external hostname we can use to access our applications.


### types
- `Ingress` - Kubernetes Ingress [see](http://kubernetes.io/docs/user-guide/ingress/)
- `LoadBalancer` - Cloud provider external loadbalancer [see](http://kubernetes.io/docs/user-guide/load-balancer/)
- `NodePort` - Recomended for local development using minikube / minishift without Ingress or Router running [see](http://kubernetes.io/docs/user-guide/services/#type-nodeport)
- `Route` - OpenShift Route [see](https://docs.openshift.com/enterprise/3.2/dev_guide/routes.html)

```yaml
cat <<EOF | kubectl create -f -
apiVersion: "v1"
data:
  config.yml: |-
    exposer: "Ingress"
    domain: "replace.me.io"
kind: "ConfigMap"
metadata:
  name: "exposecontroller"
EOF
```

### OpenShift

Same as above but using the `oc` client binary

___NOTE___ 

If you're using OpenShift then you'll need to add a couple roles:

    oc adm policy add-cluster-role-to-user cluster-admin system:serviceaccount:default:exposecontroller
    oc adm policy add-cluster-role-to-group cluster-reader system:serviceaccounts # probably too open for all setups

## Label

Now label your service with `expose=true` in [CD Pipelines](https://blog.fabric8.io/create-and-explore-continuous-delivery-pipelines-with-fabric8-and-jenkins-on-openshift-661aa82cb45a#.lx020ys70) or with CLI...

```
kubectl label svc foo expose=true
```

__exposecontroller__ will use your `exposer` type in the configmap above to automatically watch for new services and create ingress / routes / nodeports / loadbalacers for you.

## Building

 * install [go version 1.7.1 or later](https://golang.org/doc/install)
 * type the following:
 * when using minikube or minishift expose the docker daemon to build the __exposecontroller__ image and run inside kubernetes.  e.g  `eval $(minikube docker-env)`

```
git clone git://github.com/fabric8io/exposecontroller.git $GOPATH/src/github.com/fabric8io/exposecontroller
cd $GOPATH/src/github.com/fabric8io/exposecontroller

make
```

### Running locally

Make sure you've got your kube config file set up properly (remember to `oc login` if you're using OpenShift).

    make && ./bin/exposecontroller


### Run on Kubernetes or OpenShift

* build the binary
`make`
* build docker image
`make docker`
* run in kubernetes
`kubectl create -f examples/config-map.yml -f examples/deployment.yml`

### Developing 

Glide is as the exposecontroller package management

 * install [glide](https://github.com/Masterminds/glide#install)


# Future

On startup it would be good to check if an ingress controller is already running in the cluster, if not create one in an appropriate namespace using a `nodeselector` that chooses a node with a public ip.
