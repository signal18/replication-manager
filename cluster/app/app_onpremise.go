package app

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/helloyi/go-sshclient"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

func (app *App) OnPremiseConnect() (*sshclient.Client, error) {
	conf := app.Cluster.GetConf()

	if app.Cluster.IsInFailover() {
		return nil, errors.New("OnPremise Provisioning cancel during connect")
	}
	if !conf.OnPremiseSSH {
		return nil, errors.New("onpremise-ssh disable ")
	}

	user, password := misc.SplitPair(conf.GetDecryptedValue("onpremise-ssh-credential"))

	key := app.Cluster.OnPremiseGetSSHKey()
	if password != "" {
		client, err := sshclient.DialWithPasswd(misc.Unbracket(app.GetHost())+":"+strconv.Itoa(conf.OnPremiseSSHPort), user, password)
		if err != nil {
			return nil, errors.New(fmt.Sprintf("OnPremise Provisioning via SSH %s %s", err.Error(), key))
		}
		return client, nil
	} else {
		client, err := sshclient.DialWithKey(misc.Unbracket(app.GetHost())+":"+strconv.Itoa(conf.OnPremiseSSHPort), user, key)
		if err != nil {
			return nil, errors.New("OnPremise Provisioning via SSH %s" + err.Error())
		}
		return client, nil
	}
}

func (app *App) OnPremiseProvisionBootsrapApp(client *sshclient.Client) error {
	conf := app.Cluster.GetConf()
	configurator := app.Cluster.GetConfigurator()
	adminuser := "admin"
	adminpassword := "repman"
	if user, ok := app.Cluster.GetAPIUserByUsername(adminuser); ok {
		adminpassword = user.Password
	}

	envs := "export REPLICATION_MANAGER_URL=\"" + conf.APIPublicURL + "\""
	envs += " REPLICATION_MANAGER_USER=\"" + adminuser + "\""
	envs += " REPLICATION_MANAGER_PASSWORD=\"" + adminpassword + "\""
	envs += " REPLICATION_MANAGER_HOST_NAME=\"" + app.GetHost() + "\""
	envs += " REPLICATION_MANAGER_HOST_PORT=\"" + app.GetPort() + "\""
	envs += " REPLICATION_MANAGER_CLUSTER_NAME=\"" + app.Cluster.GetName() + "\""
	cmd := envs + "&& "
	cmd += "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/repository/debian/" + app.GetType() + "/bootstrap | sh"
	if configurator.HaveDBTag("rpm") {
		cmd += "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/repository/redhat/" + app.GetType() + "/bootstrap | sh"
	}
	if configurator.HaveDBTag("package") {
		cmd += "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/package/linux/" + app.GetType() + "/bootstrap | sh"
	}

	out, err := client.Cmd(cmd).SmartOutput()
	if err != nil {
		return errors.New("OnPremise Bootsrap via SSH %s" + err.Error())
	}
	app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "OnPremise Bootsrap  : %s", string(out))
	return nil
}
