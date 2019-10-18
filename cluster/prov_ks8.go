package cluster

import (
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func int32Ptr(i int32) *int32 { return &i }
func (cluster *Cluster) K8SProvisionCluster() error {

	err := cluster.K8SProvisionOneSrvPerDB()
	err = cluster.K8SProvisionProxies()
	return err
}

func (cluster *Cluster) K8SProvisionOneSrvPerDB() error {

	for _, s := range cluster.Servers {

		go cluster.K8SProvisionDatabaseService(s)

	}
	for _, s := range cluster.Servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Provisionning error %s on  %s", err, cluster.Name+"/svc/"+s.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Provisionning done for database %s", cluster.Name+"/svc/"+s.Name)
			}
		}
	}

	return nil
}

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
