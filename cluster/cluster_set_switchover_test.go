package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestSetSwitchoverWaitWriteQuery(t *testing.T) {
	c := &Cluster{Conf: &config.Config{SwitchWaitWrite: 10}}
	if err := c.SetSwitchoverWaitWriteQuery("3600"); err != nil || c.Conf.SwitchWaitWrite != 3600 {
		t.Fatalf("3600 must be accepted, got err=%v value=%d", err, c.Conf.SwitchWaitWrite)
	}
	for _, bad := range []string{"0", "-5", "abc", ""} {
		if err := c.SetSwitchoverWaitWriteQuery(bad); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
	if c.Conf.SwitchWaitWrite != 3600 {
		t.Fatalf("a rejected value must not change the setting, got %d", c.Conf.SwitchWaitWrite)
	}
}
