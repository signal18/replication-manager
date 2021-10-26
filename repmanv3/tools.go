package repmanv3

import (
	"fmt"
	"strings"

	"google.golang.org/grpc/codes"
)

func (s *ClusterSetting_Setting_SettingName) Legacy() string {
	return strings.ToLower(strings.ReplaceAll(s.String(), "_", "-"))
}

func (s *ClusterSetting_Switch_SwitchName) Legacy() string {
	return strings.ToLower(strings.ReplaceAll(s.String(), "_", "-"))
}

func (s *ClusterAction_Server) GetURI() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

func (s *ClusterAction_ReplicationTopology) Legacy() string {
	return strings.ToLower(strings.ReplaceAll(s.String(), "_", "-"))
}

type ContainsClusterMessage interface {
	GetClusterMessage() (*Cluster, error)
}

func (ca *ClusterAction) GetClusterMessage() (*Cluster, error) {
	if ca.Cluster == nil {
		return nil, NewError(codes.InvalidArgument, ErrClusterNotSet).Err()
	}

	return ca.Cluster, nil
}

func (ca *ClusterSetting) GetClusterMessage() (*Cluster, error) {
	if ca.Cluster == nil {
		return nil, NewError(codes.InvalidArgument, ErrClusterNotSet).Err()
	}

	return ca.Cluster, nil
}

func (c *Cluster) GetClusterMessage() (*Cluster, error) {
	if c == nil {
		return nil, NewError(codes.InvalidArgument, ErrClusterNotSet).Err()
	}

	return c, nil
}
