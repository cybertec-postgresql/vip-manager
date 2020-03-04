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
		//fmt.Println(err)
	}
	os.Args = []string{"vip-manager"}
	assertPanic(t, main, "Setting [IP] is mandatory")
	os.Args = []string{"vip-manager", "-ip", "0.0.0.0"}
	assertPanic(t, main, "Setting [network interface] is mandatory")
	os.Args = []string{"vip-manager", "-ip", "0.0.0.0", "-iface", "lo"}
	assertPanic(t, main, "Setting [key] is mandatory")
}

func assertPanic(t *testing.T, f func(), s string) {
    defer func() {
        if r := recover(); r == nil {
            t.Errorf("The code did not panic")
        } else {
		assert.Equal(t, s, r, "Panic message is not as expected")
        }
    }()
    f()
}
