package app

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

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

func (app *App) OnPremiseProvisionService(errorChan chan error) error {
	// conf := app.Cluster.GetConf()
	client, err := app.OnPremiseConnect()
	if err != nil {
		errorChan <- err
		return err
	}
	defer client.Close()
	// err = app.OnPremiseProvisionBootsrap(client)
	// if err != nil {
	// 	errorChan <- err
	// 	return err
	// }
	// out, err := client.Cmd("rm -f /etc/haproxy/haproxy.cfg").Cmd("cp -rp /bootstrap/etc/.cfg /etc//").Cmd("systemctl start  ").SmartOutput()
	// if err != nil {
	// 	errorChan <- err
	// 	return err
	// }
	// app.app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "OnPremise Provisioning  : %s", string(out))
	errorChan <- nil
	return nil
}

func (app *App) OnPremiseUnprovisionService(errorChan chan error) {

	errorChan <- nil

}

func (app *App) OnPremiseStopService(errorChan chan error) error {
	conf := app.Cluster.GetConf()
	var strOut string
	var err error
	app.SetWaitStartCookie()
	client, err := app.OnPremiseConnect()
	if err != nil {
		return err
	}
	defer client.Close()
	if app.AppConfig.OnPremiseStopScript == "" {
		out, err := client.Cmd("systemctl stop ").SmartOutput()
		if err != nil {
			return err
		}
		strOut = string(out)
	} else {
		var stdout, stderr bytes.Buffer

		srcpath := app.AppConfig.OnPremiseStopScript
		filerc, err2 := os.Open(srcpath)
		if err2 != nil {
			app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModApp, config.LvlErr, "Failed to load start script %s for SSH, err : %s", srcpath, err2.Error())
			return err2
		}
		defer filerc.Close()

		envBuf := strings.NewReader(app.GetSshEnv())
		r := io.MultiReader(envBuf, filerc)

		if err = client.Shell().SetStdio(r, &stdout, &stderr).Start(); err != nil {
			return err
		}
		strOut = stdout.String()
	}
	app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "OnPremise Stop   : %s", strOut)
	return nil
}

func (app *App) OnPremiseStartService(errorChan chan error) error {
	conf := app.Cluster.GetConf()
	var strOut string
	var err error
	app.SetWaitStartCookie()
	client, err := app.OnPremiseConnect()
	if err != nil {
		return err
	}
	defer client.Close()
	if app.AppConfig.OnPremiseStartScript == "" {
		out, err := client.Cmd("systemctl start ").SmartOutput()
		if err != nil {
			return err
		}
		strOut = string(out)
	} else {
		var stdout, stderr bytes.Buffer

		srcpath := app.AppConfig.OnPremiseStartScript
		filerc, err2 := os.Open(srcpath)
		if err2 != nil {
			app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModApp, config.LvlErr, "Failed to load start script %s for SSH, err : %s", srcpath, err2.Error())
			return err2
		}
		defer filerc.Close()

		envBuf := strings.NewReader(app.GetSshEnv())
		r := io.MultiReader(envBuf, filerc)

		if err = client.Shell().SetStdio(r, &stdout, &stderr).Start(); err != nil {
			return err
		}
		strOut = stdout.String()
	}

	app.Cluster.LogModulePrintf(conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "OnPremise start   : %s", strOut)
	return nil
}
