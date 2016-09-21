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
  cleanall = true

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
func getTestResultLabel( res bool) string {
  if res == false	{
    return "FAILED"
  } else {
   return "PASS"
  }
}

func runAllTests() bool {
  ret:=true
  logprintf("TESTING : Starting Test %s", "testSlaReplAllSlavesStop" )
  res:=  testSlaReplAllSlavesStop()
  logprintf("TESTING : End of Test %s -> %s", "testSlaReplAllSlavesStop", getTestResultLabel(res) )
  if res==false { ret=res}
  return ret
}
