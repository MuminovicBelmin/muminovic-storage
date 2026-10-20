package process

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNativeHelper(t *testing.T) {
	if os.Getenv("UPDATER_PROCESS_HELPER") != "1" {
		return
	}
	if os.Getenv("UPDATER_PROCESS_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "secret stderr")
		os.Exit(7)
	}
	for i, arg := range os.Args {
		if arg == "--" {
			json.NewEncoder(os.Stdout).Encode(os.Args[i+1:])
			os.Exit(0)
		}
	}
	os.Exit(2)
}
func TestNativeArgumentRoundTripDoesNotUseShell(t *testing.T) {
	t.Setenv("UPDATER_PROCESS_HELPER", "1")
	args := []string{"plain", "with spaces", `embedded"quote`, "apostrophe'value", `C:\with space\`, "", "$(not-evaluated)"}
	cmdArgs := append([]string{"-test.run=^TestNativeHelper$", "--"}, args...)
	b, e := (Native{}).Run(context.Background(), Command{Program: os.Args[0], Args: cmdArgs, Description: "fixture process", Timeout: time.Minute})
	if e != nil {
		t.Fatal(e)
	}
	var actual []string
	if e = json.Unmarshal(b, &actual); e != nil {
		t.Fatal(e)
	}
	if len(actual) != len(args) {
		t.Fatal(actual)
	}
	for i := range args {
		if actual[i] != args[i] {
			t.Fatal(actual)
		}
	}
}
func TestNativeFailuresDoNotExposeCredentials(t *testing.T) {
	t.Setenv("UPDATER_PROCESS_HELPER", "1")
	t.Setenv("UPDATER_PROCESS_FAIL", "1")
	_, e := (Native{}).Run(context.Background(), Command{Program: os.Args[0], Args: []string{"-test.run=^TestNativeHelper$"}, Description: "fixture failure"})
	if e == nil || strings.Contains(e.Error(), "secret stderr") {
		t.Fatal(e)
	}
}
