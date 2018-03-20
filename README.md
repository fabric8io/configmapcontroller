# configmapcontroller

This controller watches for changes to `ConfigMap` objects and performs rolling upgrades on their associated deployments,daemonsets or statefulsets  for apps which are not capable of watching the `ConfigMap` and updating dynamically.  

This is particularly useful if the `ConfigMap` is used to define environment variables - or your app cannot easily and reliably watch the `ConfigMap` and update itself on the fly. 

## How to use configmapcontroller

For an object(DaemonSet, Deployment, StatefulSet)  called `foo` have a `ConfigMap` called `foo`. Then add this annotation to your manifest:

```yaml
metadata:
  annotations:
    configmap.fabric8.io/update-on-change: "foo"
```

Then, providing `configmapcontroller` is running, whenever you edit the `ConfigMap` called `foo` the configmapcontroller will update the `Deployment`, `StatefulSet` or `DaemonSet` by labeling it and hence triggering a rolling update on the object provided that .spec.updateStrategy.type is set to `RollingUpdate`. 

The label would be

```
FABRICB_FOO_REVISION=${configMapRevision}
```

