#!/bin/bash

# Copyright AppsCode Inc. and Contributors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.



# Extract current nats.conf
kubectl get cm -n ace ace-nats-config -oyaml | yq4 e '.data."nats.conf"' - > nats.conf.tmp

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
