package inference

import (
	"context"
	"testing"
)

func TestTemperatureKey_ContextCascade(t *testing.T) {
	t.Run("no context value returns zero value", func(t *testing.T) {
		ctx := context.Background()
		val, ok := ctx.Value(TemperatureKey).(float64)
		if ok {
			t.Errorf("expected no TemperatureKey in context, got %v", val)
		}
	})

	t.Run("context value is retrievable", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), TemperatureKey, 0.7)
		val, ok := ctx.Value(TemperatureKey).(float64)
		if !ok {
			t.Fatal("expected TemperatureKey in context")
		}
		if val != 0.7 {
			t.Errorf("expected 0.7, got %v", val)
		}
	})

	t.Run("context value overrides config", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), TemperatureKey, 0.6)
		val, ok := ctx.Value(TemperatureKey).(float64)
		if !ok {
			t.Fatal("expected TemperatureKey in context")
		}
		if val != 0.6 {
			t.Errorf("expected 0.6, got %v", val)
		}
	})
}
