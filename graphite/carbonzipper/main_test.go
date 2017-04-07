// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package main

import "testing"

func TestStripComments(t *testing.T) {

	var tests = []struct {
		input  string
		output string
	}{
		{
			// no comment block
			`{
    "MaxProcs":5
}
`,
			`{
    "MaxProcs":5
}
`,
		},
		{
			// one comment block line
			`## comment block header
{
    "MaxProcs":5
}
`,
			`{
    "MaxProcs":5
}
`},
		{
			// multiple comment block lines
			`## comment block header
## ignore me
## coming up to the end
## all done
{
    "MaxProcs":5
}
`,
			`{
    "MaxProcs":5
}
`},
	}

	for i, tt := range tests {
		o := stripCommentHeader([]byte(tt.input))
		if string(o) != tt.output {
			t.Errorf("strip comment test %d failed", i)
		}
	}
}
