package app

import (
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
	containers := make([]apiv1.Container, 0)

	for _, dep := range app.Deployments {
		containers = append(containers, apiv1.Container{
			Name:  dep.Name,
			Image: dep.DockerImg,
			Ports: dep.Ports,
		})
	}

	return containers
}
