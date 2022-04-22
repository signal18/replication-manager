package cluster

import (
	"context"
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func int32Ptr(i int32) *int32 { return &i }

func (cluster *Cluster) K8SConnectAPI() (*kubernetes.Clientset, error) {

	config, err := clientcmd.BuildConfigFromFlags("", cluster.Conf.KubeConfig)

	if err != nil {
		cluster.LogPrintf(LvlErr, "Cannot load Kubernetes cluster config %s %s ", cluster.Conf.KubeConfig, err)
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Cannot init Kubernetes client API %s ", err)
		return nil, err
	}
	return clientset, err
}

func (cluster *Cluster) K8SGetNodes() ([]Agent, error) {

	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Cannot init Kubernetes client API %s ", err)
		return nil, err
	}
	nodes, err := client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	agents := []Agent{}
	for _, n := range nodes.Items {
		var agent Agent
		data, _ := json.Marshal(n)
		cluster.LogPrintf(LvlInfo, "%s\n", data)
		nodeip := n.Status.Addresses
		cluster.LogPrintf(LvlInfo, "IP %s ", nodeip[0].Address)
		agent.Id = n.Status.NodeInfo.MachineID
		agent.OsName = n.Status.NodeInfo.OperatingSystem
		agent.OsKernel = n.Status.NodeInfo.KernelVersion
		//	cluster.LogPrintf(LvlInfo, "nodes %s ", n)
		agent.CpuCores = (n.Status.Capacity.Cpu().MilliValue() / 1000)
		agent.MemBytes = n.Status.Capacity.Memory().Value()
		agent.MemFreeBytes = n.Status.Allocatable.Memory().Value()

		agent.HostName = n.Name
		agents = append(agents, agent)
	}
	return agents, err
}
