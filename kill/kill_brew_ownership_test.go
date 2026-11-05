package kill

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

func TestBrewActionsOnlyAffectSelectedRegisteredInstance(t *testing.T) {
	env := intel.Env{BrewStarted: map[string]bool{"redis": true}, LaunchdByPID: map[int]string{43: "homebrew.mxcl.redis"}}
	for _, operation := range []struct {
		name string
		run  func(scan.Listener, intel.Profile) (string, error)
	}{{"stop", Stop}, {"restart", Restart}} {
		for _, selectedPID := range []int{42, 43} {
			t.Run(fmt.Sprintf("%s/pid%d", operation.name, selectedPID), func(t *testing.T) {
				defer withStubs(t)()
				l := scan.Listener{PID: selectedPID, StartTime: "T", Command: "redis-server", CommandLine: "/opt/homebrew/Cellar/redis/8.0/bin/redis-server 127.0.0.1:6379"}
				p := intel.Make(l, env)
				if p.StopKind != intel.StopBrew || p.StopArg != "redis" {
					t.Fatal("fixture classification changed", p.StopKind, p.StopArg)
				}
				runOutput = func(string, ...string) (string, error) { return "T\n", nil }
				intel.BrewServiceKnown = func(string) bool { return true }
				intel.LaunchdLabelPID = func(label string) (int, bool) {
					if label != "homebrew.mxcl.redis" {
						t.Fatal("wrong formula job", label)
					}
					return 43, true
				}
				calls, signals := 0, 0
				termProcess = func(int) error { signals++; return nil }
				runCombined = func(name string, args ...string) (string, error) {
					calls++
					if name != "brew" || strings.Join(args, " ") != "services "+operation.name+" redis" {
						t.Fatal(name, args)
					}
					return "", nil
				}
				_, err := operation.run(l, p)
				if selectedPID == 42 {
					if err == nil || calls != 0 || signals != 0 {
						t.Fatalf("manual instance affected registered service: err=%v commands=%d signals=%d", err, calls, signals)
					}
				} else if err != nil || calls != 1 || signals != 0 {
					t.Fatalf("owned instance refused or wrong action: err=%v commands=%d signals=%d", err, calls, signals)
				}
			})
		}
	}
}

func TestBrewActionsRefuseChangedMissingAndFailedJobs(t *testing.T) {
	for _, operation := range []struct {
		name string
		run  func(scan.Listener, intel.Profile) (string, error)
	}{{"stop", Stop}, {"restart", Restart}} {
		for _, mode := range []string{"changed during formula lookup", "missing", "lookup failed"} {
			t.Run(operation.name+"/"+mode, func(t *testing.T) {
				defer withStubs(t)()
				runOutput = func(string, ...string) (string, error) { return "T\n", nil }
				currentPID := 42
				intel.BrewServiceKnown = func(string) bool {
					if mode == "changed during formula lookup" {
						currentPID = 43
					}
					return true
				}
				intel.LaunchdLabelPID = func(label string) (int, bool) {
					if label != "homebrew.mxcl.redis" {
						t.Fatal(label)
					}
					if mode == "missing" {
						return 0, false
					}
					if mode == "lookup failed" {
						return 42, false
					}
					return currentPID, true
				}
				runCombined = func(string, ...string) (string, error) {
					t.Fatal("refused ownership must not execute a manager action")
					return "", nil
				}
				termProcess = func(int) error { t.Fatal("refused ownership must not fall back to TERM"); return nil }
				_, err := operation.run(scan.Listener{PID: 42, StartTime: "T"}, intel.Profile{StopKind: intel.StopBrew, StopArg: "redis"})
				if err == nil {
					t.Fatal("unverified ownership accepted")
				}
			})
		}
	}
}

func TestBrewActionsVerifyProcessIdentityBeforeManagerLookups(t *testing.T) {
	defer withStubs(t)()
	runOutput = func(string, ...string) (string, error) { return "RECYCLED\n", nil }
	intel.BrewServiceKnown = func(string) bool { t.Fatal("formula lookup before identity guard"); return true }
	intel.LaunchdLabelPID = func(string) (int, bool) { t.Fatal("job lookup before identity guard"); return 42, true }
	runCombined = func(string, ...string) (string, error) {
		t.Fatal("recycled PID must not cause manager action")
		return "", nil
	}
	termProcess = func(int) error { t.Fatal("recycled PID must not receive TERM"); return nil }
	for _, action := range []func(scan.Listener, intel.Profile) (string, error){Stop, Restart} {
		if _, err := action(scan.Listener{PID: 42, StartTime: "T"}, intel.Profile{StopKind: intel.StopBrew, StopArg: "redis"}); err == nil {
			t.Fatal("recycled PID accepted")
		}
	}
}
