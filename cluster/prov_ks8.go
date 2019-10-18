package cluster

import (
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func int32Ptr(i int32) *int32 { return &i }
func (cluster *Cluster) K8SConnect() {
	config, err := clientcmd.BuildConfigFromFlags("", cluster.Conf.KubeConfig)

	if err != nil {
		cluster.LogPrintf(LvlErr, "Cannot load Kubernetes cluster config %s ", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Cannot init Kubernetes client API %s ", err)
	}

	deploymentsClient := clientset.AppsV1().Deployments(cluster.Name)

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
							Name:  "web",
							Image: "nginx:1.12",
							Ports: []apiv1.ContainerPort{
								{
									Name:          "http",
									Protocol:      apiv1.ProtocolTCP,
									ContainerPort: 80,
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
	}
	cluster.LogPrintf(LvlInfo, "Created deployment %q.\n", result.GetObjectMeta().GetName())
}
