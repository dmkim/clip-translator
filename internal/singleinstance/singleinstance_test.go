package singleinstance

import (
	"fmt"
	"os"
	"testing"
)

func TestAcquireDetectsSecondInstance(t *testing.T) {
	name := fmt.Sprintf(`Local\ClipTranslator.Test.%d`, os.Getpid())

	first, ok := acquire(name)
	if !ok {
		t.Fatal("first acquire failed")
	}
	defer first.Release()

	second, ok := acquire(name)
	if ok {
		t.Fatal("second acquire unexpectedly succeeded")
	}
	second.Release()

	first.Release()
	third, ok := acquire(name)
	if !ok {
		t.Fatal("acquire after release failed")
	}
	third.Release()
}
