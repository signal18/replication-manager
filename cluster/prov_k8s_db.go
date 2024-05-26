package cluster

import (
	"context"
	"strconv"

	"io/ioutil"

	"github.com/signal18/replication-manager/config"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (cluster *Cluster) K8SProvisionDatabaseService(s *ServerMonitor) {

	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}
	namespace := &apiv1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: cluster.Name}}
	_, err = client.CoreV1().Namespaces().Create(context.TODO(), namespace, metav1.CreateOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlErr, "Cannot create namespace %s ", err)
	}

	/*
			apiVersion: v1
			kind: PersistentVolume
			metadata:
				name: mysql-pv-volume
				labels:
					type: local
			spec:
				storageClassName: manual
				capacity:
					storage: 20Gi
				accessModes:
					- ReadWriteOnce
				hostPath:
					path: "/mnt/data"
			---
			apiVersion: v1
			kind: PersistentVolumeClaim
			metadata:
				name: mysql-pv-claim
			spec:
				storageClassName: manual
				accessModes:
					- ReadWriteOnce
				resources:
					requests:
						storage: 20Gi

		persistentVolumes := client.CoreV1().PersistentVolumes(cluster.Name)
		pv := &apiv1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: cluster.Name + "-" + s.Name + "-volume",
			},
			Spec: apiv1.PersistentVolumeSpec{
				StorageClassName: "manual",
				AccessModes:      {apiv1.ReadWriteOnce},
				Resources: apiv1.ResourceRequirements{
					Requests: apiv1.ResourceList{
						api.ResourceName(api.ResourceStorage): resource.MustParse("1Gi"),
					},
				},
			},
		}
		pvresult, pverr := persistentVolumes.Create(pv)
		if pverr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,LvlErr, "Cannot deploy Kubernetes pv %s ", pverr)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,LvlInfo, "Created Kubernetes physical volume %q.\n", pvresult.GetObjectMeta().GetName())
	*/
	persistentVolumeClaims := client.CoreV1().PersistentVolumeClaims(cluster.Name)
	pvc := &apiv1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: cluster.Name + "-" + s.Name + "-claim",
		},
		Spec: apiv1.PersistentVolumeClaimSpec{
			AccessModes: []apiv1.PersistentVolumeAccessMode{
				apiv1.ReadWriteOnce,
			},
			Resources: apiv1.VolumeResourceRequirements{
				Requests: apiv1.ResourceList{
					apiv1.ResourceName(apiv1.ResourceStorage): resource.MustParse("1Gi"),
				},
			},
		},
	}
	pvcresult, pvcerr := persistentVolumeClaims.Create(context.TODO(), pvc, metav1.CreateOptions{})
	if pvcerr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlErr, "Cannot deploy Kubernetes pvc %s ", pvcerr)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlInfo, "Created Kubernetes physical volume claim %q.\n", pvcresult.GetObjectMeta().GetName())

	s.GetDatabaseConfig()
	data, err := ioutil.ReadFile(s.Datadir + "/config.tar.gz")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlErr, "Provision can not found file %s ", s.Datadir+"/config.tar.gz")
	}

	configMapName := s.Name + "-config-map"
	configMap := apiv1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ConfigMap",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: cluster.Name,
		},
		BinaryData: map[string][]byte{
			"config.tar.gz": data,
		},
	}

	//var cm *apiv1.ConfigMap
	_, err = client.CoreV1().ConfigMaps(cluster.Name).Create(context.TODO(), &configMap, metav1.CreateOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlErr, "Can not provision config map  %s ", err)
	}
	deploymentsClient := client.AppsV1().Deployments(cluster.Name)

	port, _ := strconv.Atoi(s.Port)
	agent, err := cluster.GetDatabaseAgent(s)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlErr, "Can not provision database  %s ", err)
		cluster.errorChan <- err
		return
	}
	var cmd []string
	cmd = append(cmd, "sh -c 'wget -qO- http://"+cluster.Conf.MonitorAddress+":"+cluster.Conf.HttpPort+"/api/clusters/"+cluster.Name+"/servers/"+s.Name+"/"+s.Port+"/config|tar xzvf - -C /data'")
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: s.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "repication-manager",
					"tag": s.Name,
				},
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "repication-manager",
						"tag": s.Name,
					},
				},
				Spec: apiv1.PodSpec{
					Hostname: s.Name,
					NodeName: agent.HostName,
					InitContainers: []apiv1.Container{
						{
							Name:    s.Name + "-init",
							Image:   "alpine",
							Command: cmd,
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      s.Name + "-persistent-storage",
									MountPath: "/data",
								},
							},
						},
					},
					Containers: []apiv1.Container{
						{
							Name:  s.Name,
							Image: cluster.Conf.ProvDbImg,
							Ports: []apiv1.ContainerPort{
								{
									Name:          "mysql",
									Protocol:      apiv1.ProtocolTCP,
									ContainerPort: int32(port),
								},
							},
							Env: []apiv1.EnvVar{
								{
									Name:  "MYSQL_ROOT_PASSWORD",
									Value: s.Pass,
								},
							},
							VolumeMounts: []apiv1.VolumeMount{
								{
									Name:      s.Name + "-persistent-storage",
									MountPath: "/var/lib/mysql",
								},
							},
						},
					},
					Volumes: []apiv1.Volume{
						{
							Name: s.Name + "-persistent-storage",
							VolumeSource: apiv1.VolumeSource{
								PersistentVolumeClaim: &apiv1.PersistentVolumeClaimVolumeSource{
									ClaimName: cluster.Name + "-" + s.Name + "-claim",
								},
							},
						},
					},
				},
			},
		},
	}

	// Create Deployment
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlInfo, "Creating Kubernetes deployment...")
	result, err := deploymentsClient.Create(context.TODO(), deployment, metav1.CreateOptions{})
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlErr, "Cannot deploy Kubernetes deployment %s ", err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlInfo, "Created Kubernetes deployment %q.\n", result.GetObjectMeta().GetName())
	servicesClient := client.CoreV1().Services(cluster.Name)

	service := &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name: s.Name,
		},
		Spec: apiv1.ServiceSpec{
			Ports: []apiv1.ServicePort{
				{
					Name:     "mysql",
					Protocol: apiv1.ProtocolTCP,
					Port:     int32(port),
				},
			},
			//			ClusterIP: "",
			Selector: map[string]string{
				"app": "repication-manager",
				"tag": s.Name,
			},
		},
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlInfo, "Creating service...")
	result2, err2 := servicesClient.Create(context.TODO(), service, metav1.CreateOptions{})
	if err2 != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlErr, "Cannot deploy Kubernetes service %s ", err2)
		cluster.errorChan <- err2
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlInfo, "Created Kubernetes service %s.\n", result2.GetObjectMeta().GetName())
	cluster.errorChan <- nil
}

func (cluster *Cluster) K8SStopDatabaseService(s *ServerMonitor) error {
	return nil
}

func (cluster *Cluster) K8SStartDatabaseService(s *ServerMonitor) error {
	return nil
}

func (cluster *Cluster) K8SUnprovisionDatabaseService(s *ServerMonitor) {
	client, err := cluster.K8SConnectAPI()
	deploymentsClient := client.AppsV1().Deployments(cluster.Name)

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}

	deletePolicy := metav1.DeletePropagationForeground
	if err := deploymentsClient.Delete(context.TODO(), s.Name, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlErr, "Cannot delete Kubernetes deployment %s %s ", s.Name, err)
		cluster.errorChan <- err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlInfo, "Deleted Kubernetes deployment %s.", s.Name)
	servicesClient := client.CoreV1().Services(cluster.Name)
	if err := servicesClient.Delete(context.TODO(), s.Name, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlErr, "Cannot delete Kubernetes service %s %s ", s.Name, err)
		cluster.errorChan <- err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlInfo, "Deleted Kubernetes service %s.", s.Name)
	cluster.errorChan <- nil

}
