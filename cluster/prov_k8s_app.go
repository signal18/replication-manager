package cluster

import (
	"context"
	"errors"

	"github.com/signal18/replication-manager/cluster/app"
	"github.com/signal18/replication-manager/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (cluster *Cluster) K8SProvisionAppService(apl *app.App) {
	clientset, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}

	deploymentsClient := clientset.AppsV1().Deployments(cluster.Name)
	deployment := apl.GetK8SDeployment()

	// Create Deployment
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Creating deployment...")
	result, err := deploymentsClient.Create(context.TODO(), deployment, metav1.CreateOptions{})

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot deploy Kubernetes service %s ", err)
		cluster.errorChan <- err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Created deployment %q.\n", result.GetObjectMeta().GetName())
	cluster.errorChan <- nil
	return
}

func (cluster *Cluster) K8SUnprovisionAppService(apl *app.App) {
	cluster.errorChan <- nil
}

func (cluster *Cluster) K8SStartAppService(server *app.App) error {
	return errors.New("Can't start app")
}
func (cluster *Cluster) K8SStopAppService(server *app.App) error {
	return errors.New("Can't stop app")
}
