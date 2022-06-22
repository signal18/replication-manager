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

func (s *Server) GetURI() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

func (s *ClusterAction_ReplicationTopology) Legacy() string {
	return strings.ToLower(strings.ReplaceAll(s.String(), "_", "-"))
}

type ContainsClusterMessage interface {
	GetClusterMessage() (*Cluster, error)
}

func (da *DatabaseStatus) GetClusterMessage() (*Cluster, error) {
	if da.Cluster == nil {
		return nil, NewError(codes.InvalidArgument, ErrClusterNotSet).Err()
	}

	return da.Cluster, nil
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

func (ca *ClusterTest) GetClusterMessage() (*Cluster, error) {
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

func (t *TableAction) GetClusterMessage() (*Cluster, error) {
	if t.Cluster == nil {
		return nil, NewError(codes.InvalidArgument, ErrClusterNotSet).Err()
	}

	return t.Cluster, nil
}

func (t *TableAction) Validate() error {
	if t.Table.TableName == "" {
		return NewErrorResource(codes.InvalidArgument, ErrFieldNotSet, "table_name", "").Err()
	}
	if t.Table.TableSchema == "" {
		return NewErrorResource(codes.InvalidArgument, ErrFieldNotSet, "table_schema", "").Err()
	}

	return nil
}
