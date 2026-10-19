package misc

import "syscall"

func statCtime(st *syscall.Stat_t) syscall.Timespec { return st.Ctimespec }
