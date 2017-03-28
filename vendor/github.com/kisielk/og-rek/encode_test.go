package ogórek

import (
	"bytes"
	"io"
	"reflect"
	"testing"
)

func TestEncode(t *testing.T) {

	type foo struct {
		Foo string
		Bar int32
	}

	tests := []struct {
		name   string
		input  interface{}
		output interface{}
	}{
		{
			"graphite message",
			[]interface{}{map[interface{}]interface{}{"values": []interface{}{float64(473), float64(497), float64(540), float64(1497), float64(1808), float64(1890), float64(2013), float64(1821), float64(1847), float64(2176), float64(2156), float64(1250), float64(2055), float64(1570), None{}, None{}}, "start": int64(1383782400), "step": int64(86400), "end": int64(1385164800), "name": "ZZZZ.UUUUUUUU.CCCCCCCC.MMMMMMMM.XXXXXXXXX.TTT"}},
			nil,
		},
		{
			"small types",
			[]interface{}{int64(0), int64(1), int64(258), int64(65537), false, true},
			nil,
		},
		{
			"array of struct types",
			[]foo{{"Qux", 4}},
			[]interface{}{map[interface{}]interface{}{"Foo": "Qux", "Bar": int64(4)}},
		},
	}

	for _, tt := range tests {
		p := &bytes.Buffer{}
		e := NewEncoder(p)
		err := e.Encode(tt.input)
		if err != nil {
			t.Errorf("%s: encode error: %v", tt.name, err)
		}

		d := NewDecoder(bytes.NewReader(p.Bytes()))
		output, _ := d.Decode()

		want := tt.output
		if want == nil {
			want = tt.input
		}

		if !reflect.DeepEqual(want, output) {
			t.Errorf("%s: got\n%q\n expected\n%q", tt.name, output, want)
		}

		for l := int64(p.Len())-1; l >= 0; l-- {
			p.Reset()
			e := NewEncoder(LimitWriter(p, l))
			err = e.Encode(tt.input)
			if err != io.EOF {
				t.Errorf("%s: encoder did not handle write error @%v: got %#v", tt.name, l, err)
			}
		}

	}
}

// like io.LimitedReader but for writes
// XXX it would be good to have it in stdlib
type LimitedWriter struct {
	W io.Writer
	N int64
}

func (l *LimitedWriter) Write(p []byte) (n int, err error) {
	if l.N <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > l.N {
		p = p[0:l.N]
	}
	n, err = l.W.Write(p)
	l.N -= int64(n)
	return
}

func LimitWriter(w io.Writer, n int64) io.Writer { return &LimitedWriter{w, n} }
