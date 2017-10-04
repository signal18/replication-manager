package struct_export_test

type struct0 struct {
	Joker     string
	Killer    float32
	Duplicate int
}

type Struct1 struct {
	struct0
	Lawnmower          uint8
	Duplicate          float64
	shouldntMessWithMe int
	likewise           int `pickle:"foobar"`
}
