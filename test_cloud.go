package main

import (
	"context"
	"fmt"
	"tzro/internal/inference"
)

func main() {
	schema := `{"type":"object","properties":{"ready":{"type":"boolean"},"reason":{"type":"string"},"additionalSteps":{"type":"integer"}},"required":["ready"]}`

	res, err := inference.CallCloudModel(context.Background(), []inference.InferenceMessage{
		{Role: "user", Content: "Is 42 ready?"},
	}, schema)

	if err != nil {
		fmt.Println("Error:", err)
	} else {
		fmt.Println("Result:", res)
	}
}
