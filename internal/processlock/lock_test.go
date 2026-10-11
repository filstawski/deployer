package processlock

import "testing"

func TestOnlyOneControllerCanOwnState(t *testing.T) {
	dir := t.TempDir()
	first, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := Acquire(dir); err == nil {
		second.Close()
		t.Fatal("two controllers acquired one state directory")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Acquire(dir)
	if err != nil {
		t.Fatalf("lock not released after shutdown: %v", err)
	}
	restarted.Close()
}
