# Kubernetes Demo: MariaDB Replication with Replication Manager

This demo deploys a MariaDB replication cluster managed by Replication Manager in Kubernetes using the MariaDB Operator.

## Architecture

The deployment creates:

- **MariaDB Cluster**: 2-node MariaDB replication setup using the MariaDB Operator
  - `mariadb-repl-0` - Primary/Master node
  - `mariadb-repl-1` - Replica/Slave node
- **Replication Manager**: Monitors and manages the MariaDB cluster
  - Web dashboard on port 10001
  - Automatic failover detection and orchestration

## Prerequisites

- Kubernetes cluster (minikube, kind, or any K8s cluster)
- kubectl configured
- MariaDB Operator installed

### Install MariaDB Operator

```bash
# Install the MariaDB operator (Helm charts)
helm repo add mariadb-operator https://mariadb-operator.github.io/mariadb-operator
helm install mariadb-operator-crds mariadb-operator/mariadb-operator-crds
helm install mariadb-operator mariadb-operator/mariadb-operator
```

## Deployment

### 1. Deploy the Stack

```bash
kubectl apply -f mariadb-repl-repman.yaml
```

This creates:
- Secret with MariaDB root credentials (user: `root`, password: `mariadb`)
- MariaDB StatefulSet with 2 replicas
- Replication Manager Deployment
- NodePort service for Replication Manager (port 30001)

### 2. Wait for Pods to be Ready

```bash
# Watch MariaDB pods
kubectl get pods -w | grep mariadb-repl

# Watch replication-manager pod
kubectl get pods -w -l app=replication-manager
```

Both MariaDB pods should show `2/2 Running` and replication-manager should show `1/1 Running`.

### 3. Verify Replication

Check replication-manager logs to confirm it detected the topology:

```bash
kubectl logs -l app=replication-manager --tail=50 | grep -E "(Master|Slave|topology)"
```

You should see log entries showing:
- `mariadb-repl-0.mariadb-repl-internal` as Master
- `mariadb-repl-1.mariadb-repl-internal` as Slave

## Accessing the Dashboard

### Option 1: NodePort (Persistent)

The service is configured as NodePort on port 30001.

**For minikube:**
```bash
# Get the URL
minikube service replication-manager --url

# Or build it manually
echo "http://$(minikube ip):30001"
```

**For other Kubernetes clusters:**
```bash
# Get node IP
NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="ExternalIP")].address}')
echo "http://${NODE_IP}:30001"
```

### Option 2: Port Forward (Temporary)

```bash
kubectl port-forward svc/replication-manager 10001:10001
```

Then access: http://localhost:10001

## Configuration

### MariaDB Configuration

The MariaDB cluster is configured with:
- **Storage**: 5Gi per pod
- **Replication**: Row-based binlog replication
- **Auto-failover**: Enabled via MariaDB Operator

### Replication Manager Configuration

Key environment variables:

| Variable | Value | Description |
|----------|-------|-------------|
| `REPLICATION_MANAGER_DEFAULT_HTTP_SERVER` | `true` | Enable HTTP server |
| `REPLICATION_MANAGER_DEFAULT_HTTP_BIND_ADDRESS` | `0.0.0.0` | Bind to all interfaces |
| `REPLICATION_MANAGER_DEFAULT_HTTP_PORT` | `10001` | HTTP port |
| `REPLICATION_MANAGER_CLUSTER1_DB_SERVERS_HOSTS` | `mariadb-repl-0.mariadb-repl-internal,mariadb-repl-1.mariadb-repl-internal` | Database server hostnames |
| `REPLICATION_MANAGER_CLUSTER1_DB_SERVERS_PREFERED_MASTER` | `mariadb-repl-0.mariadb-repl-internal` | Preferred primary server |
| `REPLICATION_MANAGER_CLUSTER1_DB_SERVERS_CREDENTIAL` | `root:mariadb` | Database credentials (from secret) |
| `REPLICATION_MANAGER_CLUSTER1_DB_SERVERS_CONNECT_TIMEOUT` | `1` | Connection timeout in seconds |

## DNS Resolution

The deployment uses the **headless service** created by the MariaDB Operator for direct pod access:

- **Headless Service**: `mariadb-repl-internal` (ClusterIP: None)
- **Pod DNS Names**:
  - `mariadb-repl-0.mariadb-repl-internal`
  - `mariadb-repl-1.mariadb-repl-internal`

This allows Replication Manager to connect directly to individual MariaDB pods in the StatefulSet.

## Testing Failover

### Manual Failover Test

1. Check current topology in the dashboard
2. Simulate a failure by deleting the master pod:
   ```bash
   kubectl delete pod mariadb-repl-0
   ```
3. Watch replication-manager logs:
   ```bash
   kubectl logs -l app=replication-manager -f
   ```
4. The MariaDB Operator will recreate the pod and replication-manager will detect topology changes

### Switchover Test

Access the dashboard and use the switchover functionality to promote the slave to master.

## Troubleshooting

### DNS Resolution Issues

If you see errors like:
```
Cannot resolved DNS for host mariadb-repl-0.mariadb-repl
```

Verify the headless service exists:
```bash
kubectl get svc mariadb-repl-internal
```

The service should have `ClusterIP: None` (headless).

### Connection Issues

Check that MariaDB pods are ready:
```bash
kubectl get pods -l app.kubernetes.io/instance=mariadb-repl
```

Test connectivity from replication-manager pod:
```bash
kubectl exec -it deployment/replication-manager -- ping mariadb-repl-0.mariadb-repl-internal
```

### Check Replication Status

Connect to MariaDB and check replication:
```bash
# Connect to primary
kubectl exec -it mariadb-repl-0 -c mariadb -- mysql -uroot -pmariadb -e "SHOW MASTER STATUS\G"

# Connect to replica
kubectl exec -it mariadb-repl-1 -c mariadb -- mysql -uroot -pmariadb -e "SHOW SLAVE STATUS\G"
```

## Services Created

The MariaDB Operator creates multiple services:

| Service | Type | Purpose |
|---------|------|---------|
| `mariadb-repl` | ClusterIP | Load-balanced access to cluster |
| `mariadb-repl-internal` | ClusterIP (Headless) | Direct pod access for StatefulSet |
| `mariadb-repl-primary` | ClusterIP | Access to primary node only |
| `mariadb-repl-secondary` | ClusterIP | Access to secondary nodes only |
| `replication-manager` | NodePort | Web dashboard access |

## Cleanup

Remove all resources:

```bash
kubectl delete -f mariadb-repl-repman.yaml
```

Note: PersistentVolumeClaims may need manual cleanup:
```bash
kubectl delete pvc -l app.kubernetes.io/instance=mariadb-repl
```

## Image Information

- **MariaDB**: Managed by MariaDB Operator (uses official MariaDB images)
- **Replication Manager**: `signal18/replication-manager:latest`

## Security Notes

This is a demo configuration with default credentials:
- **Username**: `root`
- **Password**: `mariadb`

For production use:
1. Change credentials in the Secret
2. Use proper RBAC and NetworkPolicies
3. Enable TLS for replication and client connections
4. Restrict dashboard access with authentication

## Additional Resources

- [MariaDB Operator Documentation](https://github.com/mariadb-operator/mariadb-operator)
- [Replication Manager Documentation](https://github.com/signal18/replication-manager)
