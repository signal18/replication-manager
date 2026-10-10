package cluster

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/yaml"
)

// K8SDatabaseManifests is the read-only, live-fetched counterpart to
// GetDatabaseServiceConfig (OpenSVC): OpenSVC's viewer shows the template
// repman would push, but Kubernetes has no equivalent single "service
// config" -- what's actually useful is what's actually running, so every
// field here is fetched live from the API server, not re-rendered from
// k8sDatabaseDeployment/k8sDatabasePVC (#1497 gap 6).
type K8SDatabaseManifests struct {
	Deployment string `json:"deployment"`
	Service    string `json:"service"`
	PVC        string `json:"pvc"`
	Pods       string `json:"pods"`
}

// yamlOrError marshals a live-fetched object to YAML for display, or
// renders the fetch error as a YAML comment -- NotFound is expected and
// common (e.g. no PVC yet on a server that failed provisioning before that
// step), so the GUI shows why a section is empty instead of a raw 500.
func k8sManifestYAMLOrError(obj interface{}, err error) string {
	if err != nil {
		return "# " + err.Error() + "\n"
	}
	b, merr := yaml.Marshal(obj)
	if merr != nil {
		return "# error rendering manifest: " + merr.Error() + "\n"
	}
	return string(b)
}

// k8sDatabaseManifestsFromClient is the *FromClient testable half (same
// split as k8sStorageClassesFromClient/K8SGetStorageClasses) -- resource
// names/namespace/selector here must match what k8sDatabaseDeployment
// (Deployment/Service name == s.Name), k8sDatabasePVC (claim name), and the
// pod labels it sets actually create, or every section 404s against a
// genuinely-provisioned server.
func (cluster *Cluster) k8sDatabaseManifestsFromClient(client kubernetes.Interface, s *ServerMonitor) K8SDatabaseManifests {
	ns := cluster.Name

	dep, err := client.AppsV1().Deployments(ns).Get(context.TODO(), s.Name, metav1.GetOptions{})
	if err == nil {
		dep.ManagedFields = nil
		dep.TypeMeta = metav1.TypeMeta{Kind: "Deployment", APIVersion: "apps/v1"}
	}
	deployment := k8sManifestYAMLOrError(dep, err)

	svc, err := client.CoreV1().Services(ns).Get(context.TODO(), s.Name, metav1.GetOptions{})
	if err == nil {
		svc.ManagedFields = nil
		svc.TypeMeta = metav1.TypeMeta{Kind: "Service", APIVersion: "v1"}
	}
	service := k8sManifestYAMLOrError(svc, err)

	pvcName := cluster.Name + "-" + s.Name + "-claim"
	pvc, err := client.CoreV1().PersistentVolumeClaims(ns).Get(context.TODO(), pvcName, metav1.GetOptions{})
	if err == nil {
		pvc.ManagedFields = nil
		pvc.TypeMeta = metav1.TypeMeta{Kind: "PersistentVolumeClaim", APIVersion: "v1"}
	}
	pvcYAML := k8sManifestYAMLOrError(pvc, err)

	pods, err := client.CoreV1().Pods(ns).List(context.TODO(), metav1.ListOptions{
		LabelSelector: "app=repication-manager,tag=" + s.Name,
	})
	if err == nil {
		for i := range pods.Items {
			pods.Items[i].ManagedFields = nil
			pods.Items[i].TypeMeta = metav1.TypeMeta{Kind: "Pod", APIVersion: "v1"}
		}
		pods.TypeMeta = metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"}
	}
	podsYAML := k8sManifestYAMLOrError(pods, err)

	return K8SDatabaseManifests{
		Deployment: deployment,
		Service:    service,
		PVC:        pvcYAML,
		Pods:       podsYAML,
	}
}

// K8SGetDatabaseManifests is the live-connecting wrapper (see
// K8SGetStorageClasses for the same pattern) -- a connection failure
// (kubeconfig unreachable, etc.) surfaces as an error comment in every
// section rather than an empty response, since the caller (the API
// handler) always returns 200 with this struct regardless.
func (cluster *Cluster) K8SGetDatabaseManifests(s *ServerMonitor) K8SDatabaseManifests {
	client, err := cluster.K8SConnectAPI()
	if err != nil {
		errText := "# " + err.Error() + "\n"
		return K8SDatabaseManifests{Deployment: errText, Service: errText, PVC: errText, Pods: errText}
	}
	return cluster.k8sDatabaseManifestsFromClient(client, s)
}
