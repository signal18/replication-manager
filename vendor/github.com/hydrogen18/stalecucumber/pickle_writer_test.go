package stalecucumber

import "testing"
import "bytes"
import "io"
import "reflect"
import "math/big"
import "github.com/hydrogen18/stalecucumber/struct_export_test"

func TestPickleBadTypes(t *testing.T) {
	c := make(chan int)
	assertPicklingFails(c,
		PicklingError{Err: ErrTypeNotPickleable, V: c},
		t)

}

func assertPicklingFails(v interface{}, expect error, t *testing.T) {
	buf := &bytes.Buffer{}
	p := NewPickler(buf)
	_, err := p.Pickle(v)
	if err == nil {
		t.Fatalf("Pickling (%T)%v should have failed", v, v)
	}

	if !reflect.DeepEqual(err, expect) {
		t.Fatalf("Expected error (%T)%v got (%T)%v", expect, expect, err, err)
	}
}

func TestPickleFloat64(t *testing.T) {
	roundTrip(1337.42, t)
}

func TestPickleByte(t *testing.T) {
	inAndOut(byte(1), int64(1), t)
}

func TestPickleFloat32(t *testing.T) {
	var f float32
	f = 1337.42
	inAndOut(f, float64(f), t)
}

func TestPickleString(t *testing.T) {
	roundTrip("test string #1", t)
}

func TestPickleInt(t *testing.T) {
	var i int
	i = 2300000

	inAndOut(i, int64(i), t)
}

func TestPickleInt8(t *testing.T) {
	var i int8
	i = 43

	inAndOut(i, int64(i), t)
}

func TestPickleInt16(t *testing.T) {
	var i int16
	i = 4200

	inAndOut(i, int64(i), t)
}

func TestPickleInt32(t *testing.T) {
	var i int32
	i = 42

	inAndOut(i, int64(i), t)
}

func TestPickleUint(t *testing.T) {
	var i uint
	i = 13556263

	inAndOut(i, int64(i), t)
}

func TestPickleUint8(t *testing.T) {
	var i uint8
	i = 22

	inAndOut(i, int64(i), t)
}

func TestPickleUint16(t *testing.T) {
	var i uint16
	i = 10000

	inAndOut(i, int64(i), t)
}

func TestPickleUint32(t *testing.T) {
	var i uint32
	i = 2
	inAndOut(i, int64(i), t)

	i = 4294967295
	bi := big.NewInt(int64(i))
	inAndOut(i, bi, t)
}

func TestPickleUint64(t *testing.T) {
	var i uint64
	i = 1580137

	inAndOut(i, int64(i), t)

	i = 18446744073709551615

	/**
	buf := &bytes.Buffer{}
	p := NewPickler(buf)
	_, err := p.Pickle(i)
	if err != nil {
		t.Fatal(err)
	}

	t.Fatalf("%v", buf.Bytes())
	**/

	bi := big.NewInt(0)
	bi.SetUint64(i)
	inAndOut(i, bi, t)

	var ui uint
	ui = ^uint(0)
	bi.SetUint64(uint64(ui))
	inAndOut(ui, bi, t)
}

func TestPickleInt64(t *testing.T) {
	var i int64
	i = 1337

	roundTrip(i, t)

	i = 1 << 48
	inAndOut(i, big.NewInt(i), t)

	i *= -1
	inAndOut(i, big.NewInt(i), t)

	i = 1 << 32
	i *= -1
	inAndOut(i, big.NewInt(i), t)
}

func TestPickleSlice(t *testing.T) {
	data := make([]interface{}, 3)
	data[0] = "meow"
	data[1] = int64(1336)
	data[2] = float64(42.0)
	roundTrip(data, t)

	var array [3]int
	out := make([]interface{}, len(array))
	array[0] = 100
	array[1] = 200
	array[2] = 300
	for i, v := range array {
		out[i] = int64(v)
	}
	inAndOut(array, out, t)
}

func TestPickleMap(t *testing.T) {
	data := make(map[interface{}]interface{})
	data[int64(1)] = int64(10)
	data[int64(2)] = int64(20)
	data[int64(3)] = "foobar"
	data[int64(4)] = float64(4.0)
	data[int64(5)] = float64(2.0)
	data["meow"] = "foooooocool"
	roundTrip(data, t)

	in := make(map[string]float32)
	out := make(map[interface{}]interface{})
	in["foo"] = 2.0
	out["foo"] = 2.0

	in["bar"] = 4.0
	out["bar"] = 4.0

	inAndOut(in, out, t)
}

type exampleStruct struct {
	Apple    int
	Banana   int32
	Cat      uint32
	Dog      int8
	Elephant string
	Fart     float32 `pickle:"fart"`
	Golf     uint64
}

type exampleEmbeddedStruct struct {
	exampleStruct
	Hiking string
}

func TestRoundTripStruct(t *testing.T) {
	in := exampleStruct{
		1,
		2,
		3,
		4,
		"hello world!",
		1151356.0,
		9223372036854775807,
	}

	inAndUnpack(in, t)

	in2 := struct_export_test.Struct1{}

	in2.Joker = "meow"
	in2.Killer = 23.37
	in2.Lawnmower = 23
	in2.Duplicate = 42.12

	inAndUnpack(in2, t)

	in3 := exampleEmbeddedStruct{
		exampleStruct: in,
		Hiking:        "is fun",
	}

	inAndUnpack(in3, t)
}

func TestPickleSnowman(t *testing.T) {
	roundTrip("This is a snowman: ☃", t)
}

func TestPicklePointer(t *testing.T) {
	var myptr *int

	myptr = new(int)
	*myptr = 42

	inAndUnpack(myptr, t)

	inAndUnpack(&myptr, t)

	myptr = nil
	inAndUnpack(&myptr, t)
}

func TestPickleStruct(t *testing.T) {
	example := struct {
		Apple  uint64
		Banana float32
		C      string
		dog    func(int) //Not exported, ignored by pickler
	}{
		1,
		2,
		"hello pickles",
		nil,
	}

	out := make(map[interface{}]interface{})
	out["Apple"] = int64(1)
	out["Banana"] = float64(2.0)
	out["C"] = example.C

	inAndOut(example, out, t)

	example2 := struct {
		Elephant int
		Fart     string `pickle:"fart"`
		Golf     float32
	}{
		14,
		"woohoo",
		13.37,
	}

	out = make(map[interface{}]interface{})
	out["Elephant"] = int64(example2.Elephant)
	out["Golf"] = float64(example2.Golf)
	out["fart"] = example2.Fart

	inAndOut(example2, out, t)
}

func TestPickleBool(t *testing.T) {
	var b bool

	roundTrip(b, t)

	b = true

	roundTrip(b, t)
}

func TestPickleBigInt(t *testing.T) {
	i := big.NewInt(1)
	i.Lsh(i, 42)

	roundTrip(i, t)

	i.SetUint64(1)
	i.Lsh(i, 256*8)

	roundTrip(i, t)

	i.Mul(i, big.NewInt(-1))
	roundTrip(i, t)

	i = big.NewInt(0)
	roundTrip(i, t)
}

func TestPickleTuple(t *testing.T) {
	myTuple := NewTuple(1, 2, "foobar")

	buf := &bytes.Buffer{}
	pickler := NewPickler(buf)
	_, err := pickler.Pickle(myTuple)
	if err != nil {
		t.Fatal(err)
	}

	out := []interface{}{int64(1),
		int64(2),
		"foobar"}

	//Verify it can be read back
	inAndOut(myTuple, out, t)

	myTuple = NewTuple()
	out = []interface{}{}
	inAndOut(myTuple, out, t)

	myTuple = NewTuple("a")
	out = []interface{}{"a"}
	inAndOut(myTuple, out, t)

	myTuple = NewTuple("a", "b")
	out = []interface{}{"a", "b"}
	inAndOut(myTuple, out, t)

	myTuple = NewTuple("a", "b", "c", nil, "d")
	out = []interface{}{"a", "b", "c", PickleNone{}, "d"}
	inAndOut(myTuple, out, t)
}

func inAndUnpack(v interface{}, t *testing.T) {
	buf := &bytes.Buffer{}

	p := NewPickler(buf)
	_, err := p.Pickle(v)
	if err != nil {
		t.Fatalf("Failed writing type %T:\n%v", v, err)
	}

	w := reflect.New(reflect.TypeOf(v))
	err = UnpackInto(w.Interface()).From(Unpickle(bytes.NewReader(buf.Bytes())))
	if err != nil {
		t.Fatalf("Failed unpickling %T:%v", w, err)
	}

	wi := reflect.Indirect(w).Interface()
	if !reflect.DeepEqual(v, wi) {
		t.Fatalf("\nFrom:%x\n---EXPECTED:(%T)\n%v\n---GOT:(%T)\n%v", buf.Bytes(), v, v, wi, wi)
	}

}

func inAndOut(v, w interface{}, t *testing.T) {
	buf := &bytes.Buffer{}

	p := NewPickler(buf)
	_, err := p.Pickle(v)
	if err != nil {
		t.Fatalf("Failed writing type %T:%v", v, err)
	}

	sanityCheck(buf, t, w)
}

func roundTrip(v interface{}, t *testing.T) {
	buf := &bytes.Buffer{}

	p := NewPickler(buf)
	_, err := p.Pickle(v)
	if err != nil {
		t.Fatalf("Failed writing type %T:%v", v, err)
	}

	sanityCheck(buf, t, v)
}

func sanityCheck(r io.Reader, t *testing.T, expect interface{}) {
	v, err := Unpickle(r)
	if err != nil {
		t.Fatalf("Failed to unpickle own output:%v", err)
	}

	if !reflect.DeepEqual(v, expect) {
		t.Fatalf("\n---EXPECTED:(%T)\n%v\n---GOT:(%T)\n%v", expect, expect, v, v)
	}
}
