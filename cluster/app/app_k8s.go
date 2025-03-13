package app

import (
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func int32Ptr(i int32) *int32 { return &i }

func (app *App) GetK8SDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: app.Cluster.GetName() + "-deployment",
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
					Containers: app.K8SGetContainers(),
				},
			},
		},
	}
}

func (app *App) K8SGetContainers() []apiv1.Container {
	port, _ := strconv.Atoi(app.GetPort())
	containers := make([]apiv1.Container, 0)

	for section, _ := range app.DeployConfigMap {
		containers = append(containers, apiv1.Container{
			Name:  section,
			Image: app.GetAppDockerImg(section),
			Ports: []apiv1.ContainerPort{
				{
					Name:          section,
					Protocol:      apiv1.ProtocolTCP,
					ContainerPort: int32(port),
				},
			},
		})
	}

	return containers
}
