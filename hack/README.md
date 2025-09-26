# NATS Cluster Configuration Patch

This directory contains a patch to add cluster configuration to the NATS server configmap.

## Files

- `nats-cluster-configmap-patch.yaml` - Complete configmap with cluster configuration added

## Applying the Patch

To apply the cluster configuration to the NATS server:

```bash
kubectl apply -f nats-cluster-configmap-patch.yaml
```

## Restarting NATS

After applying the patch, restart the NATS statefulset to pick up the new configuration:

```bash
kubectl rollout restart statefulset/ace-nats -n ace
```

## Verification

Check that the cluster configuration is applied:

```bash
kubectl get configmap ace-nats-config -n ace -o jsonpath='{.data.nats\.conf}' | grep -A 10 "cluster {"
```

## Cluster Configuration Details

The patch adds the following cluster configuration:

```yaml
cluster {
  port: 6222
  name: "nats"

  routes = [
    "nats://ace-nats-0.ace-nats.ace.svc.cluster.local:6222",
    "nats://ace-nats-1.ace-nats.ace.svc.cluster.local:6222",
    "nats://ace-nats-2.ace-nats.ace.svc.cluster.local:6222"
  ]

  cluster_advertise: "$CLUSTER_ADVERTISE"
  connect_retries: 120
}
```

This enables NATS clustering with automatic route discovery and proper cluster advertisement.