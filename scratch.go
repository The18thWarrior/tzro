package main

import (
	"fmt"
	"tzro/internal/tools"
)

func main() {
	v := tools.NewPathValidator([]string{"/Users/jp/Desktop/Repos/tzro"})
	abs, err := v.ValidatePath("internal/observer/observer.go")
	if err != nil {
		fmt.Println("Error:", err)
	} else {
		fmt.Println("Success:", abs)
	}
}
