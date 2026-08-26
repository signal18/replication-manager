package cluster

import (
	"context"
	"errors"
	"os"
	"strconv"

	"github.com/signal18/replication-manager/config"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Never fatal: a least-privilege RBAC setup may grant no verb at all on the
// cluster-scoped "namespaces" resource, so Create() can be Forbidden even when
// the namespace already exists and provisioning would otherwise succeed. A
// genuinely missing namespace still surfaces below, at the PVC/Deployment/
// Service creates that actually need it.
func (cluster *Cluster) k8sEnsureNamespace(client kubernetes.Interface, name string) {
	namespace := &apiv1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	_, err := client.CoreV1().Namespaces().Create(context.TODO(), namespace, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "Cannot create namespace %s ", err)
	}
}

func (cluster *Cluster) K8SProvisionDatabaseService(s *ServerMonitor) {

	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}
	if cluster.Conf.APISecureConfig {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"api-credentials-secure-config is enabled: the Kubernetes DB init-container config fetch sends no credentials and will get HTTP 403, so %s will not bootstrap", s.Name)
	}
	cluster.k8sEnsureNamespace(client, cluster.Name)

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
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator,config.LvlErr, "Cannot deploy Kubernetes pv %s ", pverr)
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
	if pvcerr != nil && !apierrors.IsAlreadyExists(pvcerr) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot deploy Kubernetes pvc %s ", pvcerr)
		cluster.errorChan <- pvcerr
		return
	}
	if pvcerr == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Created Kubernetes physical volume claim %q.\n", pvcresult.GetObjectMeta().GetName())
	}

	// Not mounted or consumed by the Deployment below (bootstrap is the HTTP
	// init-container fetch further down) — best-effort only.
	s.GetDatabaseConfig()
	data, err := os.ReadFile(s.Datadir + "/config.tar.gz")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Provision can not found file %s ", s.Datadir+"/config.tar.gz")
	} else {
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

		_, cmerr := client.CoreV1().ConfigMaps(cluster.Name).Create(context.TODO(), &configMap, metav1.CreateOptions{})
		if cmerr != nil && !apierrors.IsAlreadyExists(cmerr) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not provision config map  %s ", cmerr)
		}
	}
	deploymentsClient := client.AppsV1().Deployments(cluster.Name)

	port, err := strconv.Atoi(s.Port)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Invalid database port %s: %s ", s.Port, err)
		cluster.errorChan <- err
		return
	}
	agent, err := cluster.GetDatabaseAgent(s)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not provision database  %s ", err)
		cluster.errorChan <- err
		return
	}
	nodeHostnameLabel := cluster.k8sHostnameLabel(agent.HostName)
	// No auth header: 403s if api-credentials-secure-config is enabled.
	cmd := []string{
		"sh", "-c",
		"wget -qO- http://" + cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort + "/api/clusters/" + cluster.Name + "/servers/" + s.Name + "/" + s.Port + "/config|tar xzvf - -C /data",
	}
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
					// NodeSelector, not NodeName: NodeName bypasses the scheduler
					// entirely, which means a WaitForFirstConsumer StorageClass
					// (the default for most dynamic provisioners) never binds the
					// PVC, since that only happens during scheduling. NodeSelector
					// pins the pod to the same node while still going through the
					// scheduler.
					NodeSelector: map[string]string{
						"kubernetes.io/hostname": nodeHostnameLabel,
					},
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
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Creating Kubernetes deployment...")
	result, err := deploymentsClient.Create(context.TODO(), deployment, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot deploy Kubernetes deployment %s ", err)
		cluster.errorChan <- err
		return
	}
	if err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Created Kubernetes deployment %q.\n", result.GetObjectMeta().GetName())
	}
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
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Creating service...")
	result2, err2 := servicesClient.Create(context.TODO(), service, metav1.CreateOptions{})
	if err2 != nil && !apierrors.IsAlreadyExists(err2) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot deploy Kubernetes service %s ", err2)
		cluster.errorChan <- err2
		return
	}
	if err2 == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Created Kubernetes service %s.\n", result2.GetObjectMeta().GetName())
	}
	cluster.errorChan <- nil
}

// No scale-to-zero/drain semantic exists for the Deployment; an explicit
// error here (not silent nil) makes RestartDatabaseService fail fast instead
// of timing out in WaitDatabaseFailed for a stop that never happened.
func (cluster *Cluster) K8SStopDatabaseService(s *ServerMonitor) error {
	return errors.New("stop is not supported for the kubernetes orchestrator")
}

// No real start/scale-up exists either; this only confirms the Deployment is
// present with a non-zero desired replica count before reporting success.
func (cluster *Cluster) k8sStartDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	dep, err := client.AppsV1().Deployments(cluster.Name).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot start database %s: deployment not found: %s ", name, err)
		return err
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot start database %s: %s ", name, err)
		return err
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas == 0 {
		err := errors.New("database deployment " + name + " is scaled to zero and kubernetes start does not scale it back up")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot start database %s: %s ", name, err)
		return err
	}
	return nil
}

func (cluster *Cluster) K8SStartDatabaseService(s *ServerMonitor) error {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		return err
	}
	return cluster.k8sStartDatabaseServiceWithClient(client, s.Name)
}

// ConfigMap, PVC and Namespace are intentionally retained — PVC deletion is
// destructive and retention semantics are an open question.
func (cluster *Cluster) k8sUnprovisionDatabaseServiceWithClient(client kubernetes.Interface, name string) error {
	deletePolicy := metav1.DeletePropagationForeground
	var firstErr error

	deploymentsClient := client.AppsV1().Deployments(cluster.Name)
	if err := deploymentsClient.Delete(context.TODO(), name, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && !apierrors.IsNotFound(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot delete Kubernetes deployment %s %s ", name, err)
		firstErr = err
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Deleted Kubernetes deployment %s.", name)
	}

	servicesClient := client.CoreV1().Services(cluster.Name)
	if err := servicesClient.Delete(context.TODO(), name, metav1.DeleteOptions{
		PropagationPolicy: &deletePolicy,
	}); err != nil && !apierrors.IsNotFound(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot delete Kubernetes service %s %s ", name, err)
		if firstErr == nil {
			firstErr = err
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Deleted Kubernetes service %s.", name)
	}

	return firstErr
}

// Exactly one value is ever sent on cluster.errorChan.
func (cluster *Cluster) K8SUnprovisionDatabaseService(s *ServerMonitor) {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Cannot init Kubernetes client API %s ", err)
		cluster.errorChan <- err
		return
	}
	cluster.errorChan <- cluster.k8sUnprovisionDatabaseServiceWithClient(client, s.Name)
}
