package cluster

import (
	"context"
	"encoding/json"

	"github.com/signal18/replication-manager/config"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func int32Ptr(i int32) *int32 { return &i }

func (cluster *Cluster) K8SConnectAPI() (*kubernetes.Clientset, error) {

	kconfig, err := clientcmd.BuildConfigFromFlags("", cluster.Conf.KubeConfig)

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot load Kubernetes cluster config %s %s ", cluster.Conf.KubeConfig, err)
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(kconfig)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return nil, err
	}
	return clientset, err
}

// Agent.HostName must stay the Kubernetes node name: GetAgentInOrchetrator
// matches operator-supplied prov-db-agents entries against it. hostnameLabel
// is the node's "kubernetes.io/hostname" label value, which is not guaranteed
// to equal n.Name and is what NodeSelector-based placement actually needs.
func k8sAgentFromNode(n apiv1.Node) (agent Agent, address string, hostnameLabel string) {
	if len(n.Status.Addresses) > 0 {
		address = n.Status.Addresses[0].Address
	}
	agent.Id = n.Status.NodeInfo.MachineID
	agent.OsName = n.Status.NodeInfo.OperatingSystem
	agent.OsKernel = n.Status.NodeInfo.KernelVersion
	agent.CpuCores = n.Status.Capacity.Cpu().MilliValue() / 1000
	agent.MemBytes = n.Status.Capacity.Memory().Value()
	agent.MemFreeBytes = n.Status.Allocatable.Memory().Value()
	agent.HostName = n.Name
	return agent, address, n.Labels["kubernetes.io/hostname"]
}

func (cluster *Cluster) k8sNodesFromClient(client kubernetes.Interface) ([]Agent, error) {
	nodes, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot list Kubernetes nodes %s ", err)
		return nil, err
	}
	agents := []Agent{}
	hostnameLabels := make(map[string]string, len(nodes.Items))
	for _, n := range nodes.Items {
		data, _ := json.Marshal(n)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s\n", data)
		agent, address, hostnameLabel := k8sAgentFromNode(n)
		if address != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "IP %s ", address)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "Kubernetes node %s reports no addresses", n.Name)
		}
		agents = append(agents, agent)
		hostnameLabels[n.Name] = hostnameLabel
	}
	cluster.k8sNodeHostnameLabelsMu.Lock()
	cluster.k8sNodeHostnameLabels = hostnameLabels
	cluster.k8sNodeHostnameLabelsMu.Unlock()
	return agents, nil
}

func (cluster *Cluster) K8SGetNodes() ([]Agent, error) {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return nil, err
	}
	return cluster.k8sNodesFromClient(client)
}

// k8sHostnameLabel returns the cached "kubernetes.io/hostname" label value for
// a node (populated by the last K8SGetNodes/nodes-list call), falling back to
// nodeName if it was never captured (e.g. no nodes/list has run yet) or the
// node reported no such label. This avoids a per-node nodes/get call — a
// least-privilege RBAC setup that grants nodes/list but not nodes/get still
// gets correct NodeSelector placement.
func (cluster *Cluster) k8sHostnameLabel(nodeName string) string {
	cluster.k8sNodeHostnameLabelsMu.RLock()
	label, ok := cluster.k8sNodeHostnameLabels[nodeName]
	cluster.k8sNodeHostnameLabelsMu.RUnlock()
	if ok && label != "" {
		return label
	}
	return nodeName
}
