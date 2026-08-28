package cluster

import (
	"context"
	"strconv"

	"github.com/signal18/replication-manager/config"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ktypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// Unique per proxy instance: a shared name/selector would collide across
// proxies in the same cluster.
func k8sProxyDeploymentName(clusterName, proxyName string) string {
	return clusterName + "-" + proxyName + "-deployment"
}

// Shared cluster-wide across every proxy — never auto-deleted, since no
// single-proxy-scoped operation can prove it belongs to that proxy rather
// than a different, not-yet-migrated one in the same cluster.
func k8sLegacyProxyDeploymentName(clusterName string) string {
	return clusterName + "-deployment"
}

// Read-only: never deletes, only warns. Its selector ("app: repication-manager"
// only) label-matches new per-proxy Deployments' pods too, and a proxy that
// never migrated off it would otherwise look successfully unprovisioned while
// still running. See doc/implementation/cluster/KUBERNETES_PROVISIONING.md.
func (cluster *Cluster) k8sWarnIfLegacyProxyDeploymentExists(client kubernetes.Interface) {
	name := k8sLegacyProxyDeploymentName(cluster.Name)
	if _, err := client.AppsV1().Deployments(cluster.Name).Get(context.TODO(), name, metav1.GetOptions{}); err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"Legacy Kubernetes proxy deployment %s still exists alongside per-proxy deployments — see doc/implementation/cluster/KUBERNETES_PROVISIONING.md for manual migration steps", name)
	}
}

// Only creates a Deployment: no Namespace ensure, no Service, no config
// bootstrap/ConfigMap, and ProvProxProxysqlImg is used regardless of proxy
// type. See doc/implementation/cluster/KUBERNETES_PROVISIONING.md.
func (cluster *Cluster) k8sProvisionProxyServiceWithClient(client kubernetes.Interface, prx DatabaseProxy) error {
	cluster.k8sWarnIfLegacyProxyDeploymentExists(client)

	deploymentsClient := client.AppsV1().Deployments(cluster.Name)
	port, err := strconv.Atoi(prx.GetPort())
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Invalid proxy port %s: %s ", prx.GetPort(), err)
		return err
	}
	deploymentName := k8sProxyDeploymentName(cluster.Name, prx.GetName())
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: deploymentName,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "repication-manager",
					"tag": prx.GetName(),
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "repication-manager",
						"tag": prx.GetName(),
					},
				},
				Spec: apiv1.PodSpec{
					Containers: []apiv1.Container{
						{
							Name:  prx.GetName(),
							Image: cluster.Conf.ProvProxProxysqlImg,
							Ports: []apiv1.ContainerPort{
								{
									Name:          prx.GetName(),
									Protocol:      apiv1.ProtocolTCP,
									ContainerPort: int32(port),
								},
							},
						},
					},
				},
			},
		},
	}

	// Create Deployment
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Creating deployment...")
	result, err := deploymentsClient.Create(context.TODO(), deployment, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot deploy Kubernetes service %s ", err)
		return err
	}
	if err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Created deployment %q.\n", result.GetObjectMeta().GetName())
	}
	return nil
}

func (cluster *Cluster) K8SProvisionProxyService(prx DatabaseProxy) {
	clientset, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}
	cluster.errorChan <- cluster.k8sProvisionProxyServiceWithClient(clientset, prx)
}

func (cluster *Cluster) k8sUnprovisionProxyServiceWithClient(client kubernetes.Interface, prx DatabaseProxy) error {
	deletePolicy := metav1.DeletePropagationForeground
	deploymentName := k8sProxyDeploymentName(cluster.Name, prx.GetName())
	if err := client.AppsV1().Deployments(cluster.Name).Delete(context.TODO(), deploymentName, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && !apierrors.IsNotFound(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot delete Kubernetes deployment %s %s ", deploymentName, err)
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Deleted Kubernetes deployment %s.", deploymentName)

	cluster.k8sWarnIfLegacyProxyDeploymentExists(client)
	return nil
}

func (cluster *Cluster) K8SUnprovisionProxyService(prx DatabaseProxy) {
	clientset, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}
	cluster.errorChan <- cluster.k8sUnprovisionProxyServiceWithClient(clientset, prx)
}

// k8sStopProxyServiceWithClient scales the per-proxy Deployment to 0
// replicas -- same scale-to-0 pattern as k8sStopDatabaseServiceWithClient
// (prov_k8s_db.go), never the shared legacy Deployment
// (k8sLegacyProxyDeploymentName).
func (cluster *Cluster) k8sStopProxyServiceWithClient(client kubernetes.Interface, name string) error {
	patch := []byte(`{"spec":{"replicas":0}}`)
	_, err := client.AppsV1().Deployments(cluster.Name).Patch(context.TODO(), name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot stop proxy %s: %s ", name, err)
	}
	return err
}

// K8SStopProxyService is the Kubernetes implementation of StopProxyService
// (cluster/prov.go). Does not auto-provision a missing Deployment, and never
// touches the legacy shared <cluster>-deployment.
func (cluster *Cluster) K8SStopProxyService(server DatabaseProxy) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sStopProxyServiceWithClient(client, k8sProxyDeploymentName(cluster.Name, server.GetName()))
}

// k8sStartProxyServiceWithClient scales the per-proxy Deployment back to 1
// replica -- idempotent, same as k8sStartDatabaseServiceWithClient
// (prov_k8s_db.go): a no-op if already at 1, since Start is called
// unconditionally regardless of whether Stop actually ran.
func (cluster *Cluster) k8sStartProxyServiceWithClient(client kubernetes.Interface, name string) error {
	patch := []byte(`{"spec":{"replicas":1}}`)
	_, err := client.AppsV1().Deployments(cluster.Name).Patch(context.TODO(), name, ktypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot start proxy %s: %s ", name, err)
	}
	return err
}

// K8SStartProxyService is the Kubernetes implementation of StartProxyService
// (cluster/prov.go). Does not auto-provision a missing Deployment, and never
// touches the legacy shared <cluster>-deployment.
func (cluster *Cluster) K8SStartProxyService(server DatabaseProxy) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sStartProxyServiceWithClient(client, k8sProxyDeploymentName(cluster.Name, server.GetName()))
}
