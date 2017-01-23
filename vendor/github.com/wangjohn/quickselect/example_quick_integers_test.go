package quickselect_test

import (
	"fmt"

	"github.com/wangjohn/quickselect"
)

func Example_intQuickSelect() {
	integers := []int{5, 2, 6, 3, 1, 4}
	quickselect.IntQuickSelect(integers, 3)
	fmt.Println(integers[:3])
	// Output: [2 3 1]
}
