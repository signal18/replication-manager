package logging

import (
	gologging "github.com/op/go-logging"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
	"io"
	"os"
)

type Logger struct {
	Log *gologging.Logger
}

func PrintLogo(version string) string {

	var logo = `
██╗   ██╗ █████╗ ███╗   ███╗██████╗
██║   ██║██╔══██╗████╗ ████║██╔══██╗
██║   ██║███████║██╔████╔██║██████╔╝
╚██╗ ██╔╝██╔══██║██║╚██╔╝██║██╔═══╝
 ╚████╔╝ ██║  ██║██║ ╚═╝ ██║██║
  ╚═══╝  ╚═╝  ╚═╝╚═╝     ╚═╝╚═╝
                       router
                       version ` + version + `
                       by magnetic.io
                                      `

	return logo

}

func ConfigureLog(logPath string, headless bool) *gologging.Logger {

	var log = gologging.MustGetLogger("vamp-router")
	var backend *gologging.LogBackend
	var format = gologging.MustStringFormatter(
		"%{color}%{time:15:04:05.000} %{shortpkg:.4s} %{level:.4s} ==> %{color:reset} %{message}",
	)

	// mix in the Lumberjack logger so we can have rotation on log files
	if headless {

		if len(logPath) > 0 {
			backend = gologging.NewLogBackend(io.MultiWriter(&lumberjack.Logger{
				Filename:   logPath,
				MaxSize:    50, // megabytes
				MaxBackups: 2,  //days
				MaxAge:     14,
			}), "", 0)
		}

	} else {

		if len(logPath) > 0 {
			backend = gologging.NewLogBackend(io.MultiWriter(&lumberjack.Logger{
				Filename:   logPath,
				MaxSize:    50, // megabytes
				MaxBackups: 2,  //days
				MaxAge:     14,
			}, os.Stdout), "", 0)
		}
	}

	backendFormatter := gologging.NewBackendFormatter(backend, format)
	gologging.SetBackend(backendFormatter)

	return log

}
