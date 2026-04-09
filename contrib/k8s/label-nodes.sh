#!/bin/sh
# label-nodes.sh — apply gc/ role labels to k3s nodes for scheduling.
#
# Run once after joining all nodes to the cluster.
# Edit node names to match your actual node names (kubectl get nodes).
#
# Usage: ./label-nodes.sh

set -e

SERVER_NODE="${SERVER_NODE:-mini2}"
WORKER_NODES="${WORKER_NODES:-mini3}"
EPHEMERAL_NODES="${EPHEMERAL_NODES:-laptop}"

echo "Labeling server node: $SERVER_NODE"
kubectl label node "$SERVER_NODE" gc/role=server --overwrite

for node in $WORKER_NODES; do
  echo "Labeling worker node: $node"
  kubectl label node "$node" gc/role=worker --overwrite
done

for node in $EPHEMERAL_NODES; do
  echo "Labeling ephemeral node: $node"
  kubectl label node "$node" gc/role=worker gc/ephemeral=true --overwrite
done

echo ""
echo "Node labels applied. Verify with: kubectl get nodes --show-labels"
