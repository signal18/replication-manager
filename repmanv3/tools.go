package repmanv3

import "strings"

func (s *ClusterSetting_Setting_SettingName) Legacy() string {
	return strings.ToLower(strings.ReplaceAll(s.String(), "_", "-"))
}

func (s *ClusterSetting_Switch_SwitchName) Legacy() string {
	return strings.ToLower(strings.ReplaceAll(s.String(), "_", "-"))
}
