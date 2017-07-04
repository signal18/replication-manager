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
