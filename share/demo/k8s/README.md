# Kubernetes Demo: MariaDB Replication with Replication Manager (Native Provisioning)

This demo deploys replication-manager as a **native Kubernetes orchestrator**: repman provisions and manages the MariaDB pods itself via the K8s API, without any external operator.

## Architecture

```
┌─────────────────────────────────────┐
│         Kubernetes Cluster          │
│                                     │
│  ┌──────────────────────────────┐   │
│  │   repman (Deployment)         │   │
│  │   - prov-orchestrator=kube   │   │
│  │   - http-bootstrap-button    │   │
│  │   - in-cluster config (SA)   │   │
│  │   Dashboard: NodePort 30001  │   │
│  └──────────┬───────────────────┘   │
│             │ K8s API (RBAC)        │
│             ▼                       │
│  ┌──────────────────────────────┐   │
│  │  MariaDB pods (provisioned   │   │
│  │  by repman via K8s API)      │   │
│  │  db1 → K8s node 1            │   │
│  │  db2 → K8s node 2            │   │
│  └──────────────────────────────┘   │
└─────────────────────────────────────┘
```

repman uses its `ServiceAccount` token to authenticate against the K8s API — no external kubeconfig needed.

## Prerequisites

- Kubernetes cluster (minikube, kind, or any K8s cluster)
- `kubectl` configured with cluster access
- **No external operators required**

## Configuration

### 1. Get your K8s node names

repman needs to know which nodes to schedule MariaDB pods on. Retrieve them:

```bash
kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}'
```

Example output:
```
minikube
```

or for multi-node clusters:
```
k8s-node-1
k8s-node-2
```

### 2. Update node names in the manifest

Edit `mariadb-repl-repman.yaml` and replace the `REPLICATION_MANAGER_CLUSTER1_PROV_DB_AGENTS` value with your actual node names (comma-separated):

```yaml
- name: REPLICATION_MANAGER_CLUSTER1_PROV_DB_AGENTS
  value: "k8s-node-1,k8s-node-2"
```

For a single-node cluster (e.g. minikube):
```yaml
- name: REPLICATION_MANAGER_CLUSTER1_PROV_DB_AGENTS
  value: "minikube,minikube"
```

You can also use this one-liner to patch the manifest before applying:

```bash
NODES=$(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{","}{end}' | sed 's/,$//')
sed -i "s/node1,node2/${NODES}/" mariadb-repl-repman.yaml
```

## Deployment

### 1. Apply the manifest

```bash
kubectl apply -f mariadb-repl-repman.yaml
```

This creates:
- `Secret` — MariaDB credentials (`root:mariadb`)
- `ServiceAccount` — `repman` identity in the cluster
- `ClusterRole` + `ClusterRoleBinding` — RBAC permissions for repman to manage pods
- `Deployment` — replication-manager with `prov-orchestrator=kube`
- `Service` (NodePort 30001) — dashboard access

### 2. Wait for repman to start

```bash
kubectl get pods -w -l app=replication-manager
```

The pod should reach `1/1 Running`.

## Accessing the Dashboard

### Option 1: minikube

```bash
minikube service replication-manager --url
# or
echo "http://$(minikube ip):30001"
```

### Option 2: Other clusters — NodePort

```bash
NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="ExternalIP")].address}')
echo "http://${NODE_IP}:30001"
```

### Option 3: Port forward (any cluster)

```bash
kubectl port-forward svc/replication-manager 10001:10001
```

Then access: http://localhost:10001

## Bootstrap

Once the dashboard is accessible:

1. Open the dashboard in your browser
2. Click the **Bootstrap** button for `cluster1`
3. repman will provision the MariaDB pods via the K8s API

Verify the pods are created:

```bash
kubectl get pods -n cluster1
kubectl get svc,pvc -n cluster1
```

Watch repman logs during bootstrap:

```bash
kubectl logs -l app=replication-manager -f
```

## Verifying the Cluster

Once bootstrap completes, verify the replication topology:

```bash
# Check repman detected the topology
kubectl logs -l app=replication-manager --tail=50 | grep -E "(Master|Slave|topology|cluster1)"
```

The dashboard should show:
- `db1` as Master
- `db2` as Slave
- Replication lag and status indicators

## Testing Failover

### Automatic Failover

1. Check current topology in the dashboard
2. Delete the master pod to simulate a failure:
   ```bash
   kubectl delete pod -n cluster1 -l app=db1
   ```
3. Watch repman detect the failure and promote `db2`:
   ```bash
   kubectl logs -l app=replication-manager -f
   ```

### Switchover

Use the dashboard **Switchover** button to gracefully promote the replica to master.

Or via CLI (if configured):
```bash
kubectl exec -it deployment/replication-manager -- replication-manager-cli switchover --cluster=cluster1
```

## Configuration Reference

| Environment Variable | Value | Description |
|---------------------|-------|-------------|
| `REPLICATION_MANAGER_DEFAULT_HTTP_SERVER` | `true` | Enable HTTP dashboard |
| `REPLICATION_MANAGER_DEFAULT_HTTP_BOOTSTRAP_BUTTON` | `true` | Show Bootstrap button in UI |
| `REPLICATION_MANAGER_DEFAULT_PROV_ORCHESTRATOR` | `kube` | Use K8s as provisioning backend |
| `REPLICATION_MANAGER_DEFAULT_PROV_NET_CNI` | `true` | Use CNI networking |
| `REPLICATION_MANAGER_DEFAULT_PROV_NET_CNI_CLUSTER` | `cluster.local` | K8s cluster DNS domain |
| `REPLICATION_MANAGER_CLUSTER1_DB_SERVERS_HOSTS` | `db1,db2` | Logical DB server names |
| `REPLICATION_MANAGER_CLUSTER1_PROV_DB_DOCKER_IMG` | `mariadb:10.11` | MariaDB image to provision |
| `REPLICATION_MANAGER_CLUSTER1_PROV_DB_AGENTS` | `node1,node2` | K8s node names for scheduling |
| `REPLICATION_MANAGER_CLUSTER1_PROV_DB_TAGS` | `semisync,...` | Feature tags for provisioning |

## Troubleshooting

### repman can't connect to K8s API

Check the ServiceAccount permissions:
```bash
kubectl auth can-i create pods --as=system:serviceaccount:default:repman
kubectl auth can-i list nodes --as=system:serviceaccount:default:repman
```

### Bootstrap fails — wrong node names

Verify that `PROV_DB_AGENTS` matches actual node names:
```bash
kubectl get nodes
```

### Pods not starting in cluster1 namespace

Check that the `cluster1` namespace was created by repman:
```bash
kubectl get ns cluster1
kubectl describe ns cluster1
```

If missing, repman may lack namespace-creation permissions. Check logs:
```bash
kubectl logs -l app=replication-manager --tail=100 | grep -i "error\|namespace"
```

### Connection timeout to MariaDB

Ensure CNI networking is functional and pods in different namespaces can communicate:
```bash
kubectl get pods -n cluster1 -o wide
```

## Cleanup

```bash
kubectl delete -f mariadb-repl-repman.yaml

# Remove provisioned MariaDB resources (if bootstrap was run)
kubectl delete namespace cluster1
```

## Security Notes

This demo uses default credentials (`root:mariadb`) for simplicity. For production:

1. Replace credentials in the `repman-credentials` Secret
2. Restrict `ClusterRole` to the minimum required namespaces
3. Add NetworkPolicies to isolate the MariaDB namespace
4. Enable TLS for replication and client connections
5. Restrict dashboard access with authentication

## Image Information

- **Replication Manager**: `signal18/replication-manager:latest` (must be built with `WithProvisioning`)
- **MariaDB**: `mariadb:10.11` (provisioned by repman on bootstrap)

## Additional Resources

- [Replication Manager Documentation](https://github.com/signal18/replication-manager)
- [K8s Provisioning Configuration](https://docs.signal18.io/configuration/provisioning)
