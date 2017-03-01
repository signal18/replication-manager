// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package haproxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tanji/replication-manager/misc"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// returns an error if the file was already there
func (r *Runtime) SetPid(pidfile string) error {

	//Create and empty pid file on the specified location, if not already there
	if _, err := os.Stat(pidfile); err != nil {
		emptyPid := []byte("")
		ioutil.WriteFile(pidfile, emptyPid, 0644)
		return nil
	}
	return errors.New("file already there")
}

// Reload runtime with configuration
func (r *Runtime) Reload(c *Config) error {

	pid, err := ioutil.ReadFile(c.PidFile)
	if err != nil {
		return err
	}

	/*  Setup all the command line parameters so we get an executable similar to
	    /usr/local/bin/haproxy -f resources/haproxy_new.cfg -p resources/haproxy-private.pid -sf 1234

	*/
	arg0 := "-f"
	arg1 := c.ConfigFile
	arg2 := "-p"
	arg3 := c.PidFile
	arg4 := "-D"
	arg5 := "-sf"
	arg6 := strings.Trim(string(pid), "\n")
	var cmd *exec.Cmd

	// fmt.Println(r.Binary + " " + arg0 + " " + arg1 + " " + arg2 + " " + arg3 + " " + arg4 + " " + arg5 + " " + arg6)
	// If this is the first run, the PID value will be empty, otherwise it will be > 0

	if len(arg6) > 0 {
		log.Printf("Haproxy reloading %s %s %s %s %s %s %s %s", r.Binary, arg0, arg1, arg2, arg3, arg4, arg5, arg6)
		cmd = exec.Command(r.Binary, arg0, arg1, arg2, arg3, arg4, arg5, arg6)
	} else {
		log.Printf("Haproxy starting %s %s %s %s %s %s", r.Binary, arg0, arg1, arg2, arg3, arg4)
		cmd = exec.Command(r.Binary, arg0, arg1, arg2, arg3, arg4)
	}

	var out bytes.Buffer
	cmd.Stdout = &out

	cmdErr := cmd.Run()
	if cmdErr != nil {
		return cmdErr
	}

	return nil
}

// Sets the weight of a backend
func (r *Runtime) SetWeight(backend string, server string, weight int) (string, error) {

	result, err := r.cmd("set weight " + backend + "/" + server + " " + strconv.Itoa(weight) + "\n")

	if err != nil {
		return "", err
	} else {
		return result, nil
	}

}

// Adds an ACL.
// We need to match a frontend name to an id. This is somewhat awkard.
// func (r *Runtime) SetAcl(frontend string, acl string, pattern string) (string, error) {

// 	result, err := r.cmd("add acl " + acl + pattern)

// 	if err != nil {
// 		return "", err
// 	} else {
// 		return result, nil
// 	}
// }

// Gets basic info on haproxy process
func (r *Runtime) GetInfo() (Info, *Error) {
	var Info Info
	result, err := r.cmd("show info \n")
	if err != nil {
		return Info, &Error{500, errors.New("Error getting info")}
	} else {
		result, err := misc.MultiLineToJson(result)
		if err != nil {
			return Info, &Error{500, err}
		} else {
			err := json.Unmarshal([]byte(result), &Info)
			if err != nil {
				return Info, &Error{500, err}
			} else {
				return Info, nil
			}
		}
	}

}

/* get the basic stats in CSV format
@parameter statsType takes the form of:
- all
- frontend
- backend

Returns a struct. This one is only used by the frontend API

*/

func (r *Runtime) GetJsonStats(statsType string) ([]Stats, error) {

	var Stats []Stats
	var cmdString string

	defer func() error {
		if r := recover(); r != nil {
			return errors.New("Cannot read from Haproxy socket")
		}
		return nil
	}()

	switch statsType {
	case "all":
		cmdString = "show stat -1\n"
	case "backend":
		cmdString = "show stat -1 2 -1\n"
	case "frontend":
		cmdString = "show stat -1 1 -1\n"
	case "server":
		cmdString = "show stat -1 4 -1\n"
	}

	result, err := r.cmd(cmdString)
	if err != nil {
		return Stats, err
	} else {
		result, err := misc.CsvToJson(strings.Trim(removeStatsLines(result), "# "))
		if err != nil {
			return Stats, err
		} else {
			err := json.Unmarshal([]byte(result), &Stats)
			if err != nil {
				return Stats, err
			} else {
				return Stats, nil
			}
		}

	}
}

/* get the basic stats in CSV format

@parameter statsType takes the form of:
- all
- frontend
- backend

returns a map of a map of strings with all metrics per proxy, i.e:

["my_service"]["scur"] = 0
							["slim"] = 10000
							....

*/

func (r *Runtime) GetStats(statsType string) (map[string]map[string]string, error) {

	var cmdString string
	m := make(map[string]map[string]string)

	switch statsType {
	case "all":
		cmdString = "show stat -1\n"
	case "backend":
		cmdString = "show stat -1 2 -1\n"
	case "frontend":
		cmdString = "show stat -1 1 -1\n"
	case "server":
		cmdString = "show stat -1 4 -1\n"
	}

	result, err := r.cmd(cmdString)
	if err != nil {
		return m, err
	} else {

		result, err := misc.CsvToMap(strings.Trim(removeStatsLines(result), "# "))
		return result, err
	}
}

// Executes a arbitrary HAproxy command on the unix socket
func (r *Runtime) cmd(cmd string) (string, error) {

	// connect to haproxy
	conn, err_conn := net.Dial("unix", r.SockFile)
	defer conn.Close()

	if err_conn != nil {
		return "", errors.New("Unable to connect to Haproxy socket")
	} else {

		fmt.Fprint(conn, cmd)

		response := ""

		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			response += (scanner.Text() + "\n")
		}
		if err := scanner.Err(); err != nil {
			return "", err
		} else {
			return response, nil
		}

	}
}

func (r *Runtime) Reset() *Error {

	if _, err := r.cmd("clear counters all" + "\n"); err != nil {
		return &Error{500, errors.New("Error resetting counters")}
	}
	return nil
}

func removeStatsLines(in string) string {
	rx := regexp.MustCompile("stats[,].*")
	res := rx.ReplaceAllString(in, "")
	return res
}
