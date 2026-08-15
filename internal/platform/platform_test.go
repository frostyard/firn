package platform

import (
	"errors"
	"testing"
)

func TestRequireUEFI(t *testing.T) {
	if err := RequireUEFI(true); err != nil {
		t.Fatalf("RequireUEFI(true): %v", err)
	}
	if err := RequireUEFI(false); !errors.Is(err, ErrUEFIRequired) {
		t.Fatalf("RequireUEFI(false) = %v, want ErrUEFIRequired", err)
	}
}
