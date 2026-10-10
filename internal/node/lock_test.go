package node

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Two copies on one profile would fight over the server registration; the
// second is refused, and the profile is free again once the first stops.
func TestOneCopyPerProfile(t *testing.T) {
	dir := t.TempDir()
	first, err := New(dir, Events{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- first.Run(ctx) }()
	<-first.Ready()

	second, err := New(dir, Events{})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Run(context.Background()); !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("second copy: %v, want ErrProfileInUse", err)
	}

	other, _ := New(t.TempDir(), Events{})
	otherCtx, stopOther := context.WithCancel(context.Background())
	defer stopOther()
	go other.Run(otherCtx)
	select {
	case <-other.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("a different profile couldn't run alongside")
	}

	stop()
	<-done
	third, _ := New(dir, Events{})
	thirdCtx, stopThird := context.WithCancel(context.Background())
	defer stopThird()
	errc := make(chan error, 1)
	go func() { errc <- third.Run(thirdCtx) }()
	select {
	case <-third.Ready():
	case err := <-errc:
		t.Fatalf("after the first stopped: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the profile stayed locked after the first copy stopped")
	}
}
