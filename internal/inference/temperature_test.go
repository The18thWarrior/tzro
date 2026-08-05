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

func TestPresencePenaltyKey_ContextCascade(t *testing.T) {
	t.Run("no context value returns zero value", func(t *testing.T) {
		ctx := context.Background()
		val, ok := ctx.Value(PresencePenaltyKey).(float64)
		if ok {
			t.Errorf("expected no PresencePenaltyKey in context, got %v", val)
		}
	})

	t.Run("context value is retrievable", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), PresencePenaltyKey, 1.3)
		val, ok := ctx.Value(PresencePenaltyKey).(float64)
		if !ok {
			t.Fatal("expected PresencePenaltyKey in context")
		}
		if val != 1.3 {
			t.Errorf("expected 1.3, got %v", val)
		}
	})

	t.Run("does not interfere with TemperatureKey", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), PresencePenaltyKey, 1.3)
		ctx = context.WithValue(ctx, TemperatureKey, 0.65)

		pp, ppOk := ctx.Value(PresencePenaltyKey).(float64)
		temp, tempOk := ctx.Value(TemperatureKey).(float64)

		if !ppOk || pp != 1.3 {
			t.Errorf("expected PresencePenaltyKey=1.3, got %v (ok=%v)", pp, ppOk)
		}
		if !tempOk || temp != 0.65 {
			t.Errorf("expected TemperatureKey=0.65, got %v (ok=%v)", temp, tempOk)
		}
	})
}
