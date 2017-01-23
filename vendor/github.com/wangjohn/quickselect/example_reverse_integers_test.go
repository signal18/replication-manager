package quickselect_test

import (
	"fmt"

	"github.com/wangjohn/quickselect"
)

func Example_reverseQuickSelect() {
	integers := []int{5, 2, 6, 3, 1, 4}
	quickselect.QuickSelect(quickselect.Reverse(quickselect.IntSlice(integers)), 3)
	fmt.Println(integers[:3])
	// Output: [5 6 4]
}
