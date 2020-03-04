package main

import (
	"testing"
	"os"
	"fmt"
	"github.com/stretchr/testify/assert"
)

func TestMissingArguments(t *testing.T) {
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()
	origLogFatalf := logFatalf

	// After this test, replace the original fatal function
	defer func() { logFatalf = origLogFatalf } ()

	logFatalf = func(format string, args ...interface{}) {
		var err string
		if len(args) > 0 {
			err = fmt.Sprintf(format, args)
		} else {
			err = format
		}
		panic(err)
	}
	os.Args = []string{"vip-manager"}
	//We expect this to fail with message about missing IP option
	assertPanic(t, main, "Setting [IP] is mandatory")
	os.Args = []string{"vip-manager", "-ip", "0.0.0.0"}
	//We expect this to fail with message about missing network interface option
	assertPanic(t, main, "Setting [network interface] is mandatory")
	os.Args = []string{"vip-manager", "-ip", "0.0.0.0", "-iface", "lo"}
	//We expect this to fail with message about missing key option
	assertPanic(t, main, "Setting [key] is mandatory")
}

func assertPanic(t *testing.T, f func(), s string) {
    defer func() {
        if r := recover(); r == nil {
            t.Errorf("The code did not panic")
        } else {
		//Compare the panic message with what we expect
		assert.Equal(t, s, r, "Panic message is not as expected")
        }
    }()
    f()
}
