// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main
import	"github.com/tanji/replication-manager/dbhelper"
import "time"
//import "encoding/json"
//import "net/http"

func testSlaReplAllDelay() bool {
 return false
}

func testFailoverReplAllDelayInteractive() bool {
 return false
}

func testFailoverReplAllDelayAuto() bool {
 return false
}

func testSwitchoverReplAllDelay() bool {
 return false
}

func testSlaReplAllSlavesStop() bool  {


  bootstrap()
  sme.ResetUpTime()
  time.Sleep( 3 * time.Second )
  sla1 := sme.GetUptimeFailable()
  for _, s := range slaves {
   dbhelper.StopSlave(s.Conn)
	}
  time.Sleep( 10 * time.Second )
  sla2 := sme.GetUptimeFailable()
  for _, s := range slaves {
   dbhelper.StartSlave(s.Conn)
	}
  if sla2==sla1  {
    return false
   } else {
     return true
   }
}


func testSlaReplOneSlavesStop() bool {
  for _, s := range slaves {
    dbhelper.StopSlave(s.Conn)
  }
  return false
}

func testSwitchOverAllNodes() bool  {
  maxfail = len(servers) + 1
  for i := 0; i < len(servers);  {
//  for _, sv := range servers {
     oldMasterID:=master.ServerID
     masterFailover(false)
     newMasterID:= master.ServerID
     if oldMasterID==newMasterID {
       return false
     }
  }
  time.Sleep( 3 * time.Second )
  for _, s := range slaves {
    if s.IOThread != "Yes" || s.SQLThread!="Yes" || s.MasterServerID != master.ServerID {
      return false
    }

	}

//  resp, err := http.Get("http://" + bindaddr+":"+httpport +"/servers")

  return true
}


func getTestResultLabel( res bool) string {
  if res == false	{
    return "FAILED"
  } else {
   return "PASS"
  }
}

func runAllTests() bool {
  cleanall = true
  ret:=true
  logprintf("TESTING : Starting Test %s", "testSlaReplAllSlavesStop" )
  res:=  testSlaReplAllSlavesStop()
  logprintf("TESTING : End of Test %s -> %s", "testSlaReplAllSlavesStop", getTestResultLabel(res) )
  if res==false { ret=res}
  logprintf("TESTING : Starting Test %s", "testSwitchOverAllNodes" )
  res = testSwitchOverAllNodes()
  logprintf("TESTING : End of Test %s -> %s", "testSwitchOverAllNodes", getTestResultLabel(res) )
  if res==false { ret=res}

  cleanall = false
  return ret
}
