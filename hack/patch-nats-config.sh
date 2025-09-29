#!/bin/bash

# Extract current nats.conf
kubectl get cm -n ace ace-nats-config -oyaml | yq e '.data."nats.conf"' - > nats.conf.tmp

# Add cluster configuration
cat << 'EOF' >> nats.conf.tmp

##################
#                #
# Cluster        #
#                #
##################
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
EOF

# Patch the ConfigMap
kubectl patch cm -n ace ace-nats-config --patch "$(cat << EOF
data:
  nats.conf: |
$(cat nats.conf.tmp | sed 's/^/    /')
EOF
)"

# Cleanup
rm nats.conf.tmp

echo "NATS ConfigMap updated with cluster configuration"
