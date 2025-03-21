package app

import (
	"fmt"

	apiv1 "k8s.io/api/core/v1"
)

const StateWebRunning = "WebRunning"
const StateWebDown = "WebDown"
const StateSuspect = "suspect"

type VariableMapping struct {
	Name   string   `json:"name"`
	Value  string   `json:"value"`
	Type   string   `json:"type" options:"secret|env"`
	Agents []string `json:"agents" default:"all"`
}

type PathMapping struct {
	VolumeDir string   `json:"volumedir" options:"etc|log|var"` // This will be used to create the volume mount path. It will be {deployname}/{volumedir} e.g. {volume}/deploy01/etc/{from} : {to}
	From      string   `json:"from"`
	To        string   `json:"to"`
	Type      string   `json:"type" options:"shm|direct"`
	Agents    []string `json:"agents" default:"all"`
}

type Deployment struct {
	Name          string                `json:"name"`
	Variables     []VariableMapping     `json:"variables"`
	Path          []PathMapping         `json:"path"`
	Ports         []apiv1.ContainerPort `json:"ports"`
	DockerImg     string                `json:"dockerImg"`
	DockerRunArgs string                `json:"dockerRunArgs"`
	DockerRunCmd  string                `json:"dockerRunCmd"`
	GitClones     []GitClone            `json:"gitClones"`
}

// GetPorts returns the ports in the format "hostPort:containerPort"
// if hostPort is 0, it will return only the containerPort
func (d *Deployment) GetPorts() []string {
	ports := make([]string, 0)
	for _, port := range d.Ports {
		if port.HostPort != 0 {
			ports = append(ports, fmt.Sprintf("%d:%d", port.HostPort, port.ContainerPort))
		} else {
			ports = append(ports, fmt.Sprintf("%d", port.ContainerPort))
		}
	}
	return ports
}

type GitClone struct {
	GitRepo   string `json:"repo"`
	GitBranch string `json:"branch"`
	Dest      string `json:"dest" options:"config|data"`
	GitUser   string `json:"user"`
	GitPass   string `json:"pass"`
}

type Deployments []Deployment
