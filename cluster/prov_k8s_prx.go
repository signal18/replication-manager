package cluster

import (
	"errors"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (cluster *Cluster) K8SProvisionProxyService(prx DatabaseProxy) {
	clientset, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogPrintf(LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}

	deploymentsClient := clientset.AppsV1().Deployments(cluster.Name)
	port, _ := strconv.Atoi(prx.GetPort())
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: cluster.Name + "-deployment",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "repication-manager",
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "repication-manager",
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
	cluster.LogPrintf(LvlInfo, "Creating deployment...")
	result, err := deploymentsClient.Create(deployment)

	if err != nil {
		cluster.LogPrintf(LvlErr, "Cannot deploy Kubernetes service %s ", err)
		cluster.errorChan <- err
	}
	cluster.LogPrintf(LvlInfo, "Created deployment %q.\n", result.GetObjectMeta().GetName())
	cluster.errorChan <- nil
	return
}

func (cluster *Cluster) K8SUnprovisionProxyService(prx DatabaseProxy) {
	cluster.errorChan <- nil
}

func (cluster *Cluster) K8SStartProxyService(server DatabaseProxy) error {
	return errors.New("Can't start proxy")
}
func (cluster *Cluster) K8SStopProxyService(server DatabaseProxy) error {
	return errors.New("Can't stop proxy")
}
