package stalecucumber

import "reflect"
import "testing"
import "strings"
import "math/big"

func TestHelperDictString(t *testing.T) {
	result, err := DictString(Unpickle(strings.NewReader("\x80\x02}q\x00(U\x01aq\x01K\x01K\x02K\x03u.")))

	if err == nil {
		t.Fatalf("Should not have unpickled:%v", result)
	}

	reader := strings.NewReader("\x80\x02}q\x00(U\x01aq\x01K*U\x01cq\x02U\x06foobarq\x03U\x01bq\x04G@*\xbdp\xa3\xd7\n=U\x01eq\x05\x88U\x01dq\x06\x8a\x01\x01u.")

	result, err = DictString(Unpickle(reader))
	if err != nil {
		t.Fatal(err)
	}

	expect := make(map[string]interface{})
	expect["a"] = int64(42)
	expect["b"] = 13.37
	expect["c"] = "foobar"
	expect["d"] = big.NewInt(1)
	expect["e"] = true

	if !reflect.DeepEqual(expect, result) {
		t.Fatalf("Got %v expected %v", expect, result)
	}
}

func TestIntHelper(t *testing.T) {
	result, err := Int(Unpickle(strings.NewReader("\x80\x02\x8a\x08\xff\xff\xff\xff\xff\xff\xff\x7f.")))
	if err != nil {
		t.Fatal(err)
	}

	var expect int64
	expect = 9223372036854775807
	if result != expect {
		t.Fatalf("got %d expected %d", result, expect)
	}

	expect *= -1

	result, err = Int(Unpickle(strings.NewReader("\x80\x02\x8a\x08\x01\x00\x00\x00\x00\x00\x00\x80.")))

	if err != nil {
		t.Fatal(err)
	}

	if result != expect {
		t.Fatalf("got %d expected %d", result, expect)
	}

	result, err = Int(Unpickle(strings.NewReader("\x80\x02\x8a\t\x00\x00\x00\x00\x00\x00\x00\x00\x01.")))
	if err == nil {
		t.Fatalf("should not have unpickled:%d", result)
	}
}
