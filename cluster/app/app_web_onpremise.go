package app

import (
	"bytes"
	"io"
	"os"
	"strings"

	"github.com/signal18/replication-manager/config"
)

func (app *AppWebDevOps) OnPremiseProvisionService(errorChan chan error) error {
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

func (app *AppWebDevOps) OnPremiseUnprovisionService(errorChan chan error) {

	errorChan <- nil

}

func (app *AppWebDevOps) OnPremiseStopService(errorChan chan error) error {
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

func (app *AppWebDevOps) OnPremiseStartService(errorChan chan error) error {
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
