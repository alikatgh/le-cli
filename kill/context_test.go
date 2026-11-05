package kill

import (
	"context"
	"errors"
	"testing"

	"github.com/alikatgh/le-cli/intel"
	"github.com/alikatgh/le-cli/scan"
)

// No test in this file calls StopContext/defaultContextOps: importers do not
// inherit kill's private legacy test guard, so every executor/signal is injected.
func fakeContextOps(calls *int) contextOps {
	return contextOps{
		run:     func(context.Context, string, ...string) (string, error) { *calls++; return "", nil },
		start:   func(context.Context, int) (string, error) { return "stable start", nil },
		term:    func(int) error { *calls++; return nil },
		brew:    func(context.Context, string) (bool, error) { return true, nil },
		docker:  func(context.Context, string) (string, bool, error) { return "abcdef123456", true, nil },
		launchd: func(context.Context, string) (int, bool, error) { return 42, true, nil },
	}
}

func TestContextActionsRefuseUnsafeIdentityAndStrategies(t *testing.T) {
	base := scan.Listener{PID: 42, StartTime: "stable start", CommandLine: "node server.js"}
	for _, tc := range []struct {
		name    string
		l       scan.Listener
		p       intel.Profile
		restart bool
		alter   func(*contextOps)
	}{
		{"missing start", scan.Listener{PID: 42, CommandLine: "node server.js"}, intel.Profile{StopKind: intel.StopTerm}, false, nil},
		{"recycled", base, intel.Profile{StopKind: intel.StopTerm}, false, func(op *contextOps) {
			op.start = func(context.Context, int) (string, error) { return "new start", nil }
		}},
		{"unknown strategy", base, intel.Profile{StopKind: ""}, false, nil},
		{"avoid", base, intel.Profile{StopKind: intel.StopAvoid}, false, nil},
		{"terminal restart", base, intel.Profile{StopKind: intel.StopTerm}, true, nil},
		{"missing docker ID", base, intel.Profile{StopKind: intel.StopDocker, StopArg: "db"}, false, nil},
		{"changed docker", base, intel.Profile{StopKind: intel.StopDocker, StopArg: "db", StopArgID: "000000000000"}, false, nil},
		{"changed launchd", base, intel.Profile{StopKind: intel.StopLaunchd, StopArg: "local.agent"}, false, func(op *contextOps) {
			op.launchd = func(context.Context, string) (int, bool, error) { return 43, true, nil }
		}},
		{"brew failure", base, intel.Profile{StopKind: intel.StopBrew, StopArg: "redis"}, false, func(op *contextOps) {
			op.brew = func(context.Context, string) (bool, error) { return true, errors.New("unavailable") }
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			op := fakeContextOps(&calls)
			if tc.alter != nil {
				tc.alter(&op)
			}
			_, err := actionContext(context.Background(), tc.l, tc.p, tc.restart, op)
			if !errors.Is(err, ErrRefused) || calls != 0 {
				t.Fatalf("err=%v side effects=%d", err, calls)
			}
		})
	}
}

func TestContextDockerQuarantineHasNoLookupOrMutation(t *testing.T) {
	for _, restart := range []bool{false, true} {
		calls := 0
		op := fakeContextOps(&calls)
		op.start = func(context.Context, int) (string, error) { calls++; return "stable start", nil }
		op.docker = func(context.Context, string) (string, bool, error) { calls++; return "abcdef123456", true, nil }
		l := scan.Listener{PID: 42, StartTime: "stable start"}
		p := intel.Profile{StopKind: intel.StopDocker, StopArg: "db", StopArgID: "abcdef123456"}
		if _, err := actionContext(context.Background(), l, p, restart, op); !errors.Is(err, ErrRefused) || calls != 0 {
			t.Fatalf("restart=%v err=%v calls=%d", restart, err, calls)
		}
	}
}

func TestContextDockerHelperRejectsStaleProfilesBeforeIdentityLookup(t *testing.T) {
	for _, kind := range []intel.StopKind{intel.StopTerm, intel.StopBrew, intel.StopLaunchd} {
		for _, restart := range []bool{false, true} {
			calls := 0
			op := fakeContextOps(&calls)
			op.start = func(context.Context, int) (string, error) { calls++; return "stable start", nil }
			op.brew = func(context.Context, string) (bool, error) { calls++; return true, nil }
			op.launchd = func(context.Context, string) (int, bool, error) { calls++; return 42, true, nil }
			l := scan.Listener{PID: 42, Command: "docker-proxy", CommandLine: "/usr/bin/docker-proxy -host-port 3000", StartTime: "stable start"}
			if _, err := actionContext(context.Background(), l, intel.Profile{StopKind: kind, StopArg: "fixture"}, restart, op); !errors.Is(err, ErrRefused) || calls != 0 {
				t.Fatalf("kind=%s restart=%v err=%v calls=%d", kind, restart, err, calls)
			}
		}
	}
}

func TestContextCancellationFailsClosed(t *testing.T) {
	calls := 0
	op := fakeContextOps(&calls)
	l := scan.Listener{PID: 42, StartTime: "stable start"}
	ctx, cancel := context.WithCancel(context.Background())
	op.brew = func(context.Context, string) (bool, error) { cancel(); return true, nil }
	if _, err := actionContext(ctx, l, intel.Profile{StopKind: intel.StopBrew, StopArg: "redis"}, false, op); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal(err, calls)
	}
}

func TestContextBrewBindsSelectedPIDToFormulaJob(t *testing.T) {
	// Both listeners use the same real classification path and formula. Only
	// PID 43 is actually owned by the registered current-user launchd job.
	env := intel.Env{BrewStarted: map[string]bool{"redis": true}, LaunchdByPID: map[int]string{43: "homebrew.mxcl.redis"}}
	for _, restart := range []bool{false, true} {
		for _, selectedPID := range []int{42, 43} {
			l := scan.Listener{PID: selectedPID, StartTime: "stable start", Command: "redis-server", CommandLine: "/opt/homebrew/Cellar/redis/8.0/bin/redis-server 127.0.0.1:6379"}
			p := intel.Make(l, env)
			if p.StopKind != intel.StopBrew || p.StopArg != "redis" {
				t.Fatal("fixture classification changed", p.StopKind, p.StopArg)
			}
			calls := 0
			op := fakeContextOps(&calls)
			op.launchd = func(ctx context.Context, label string) (int, bool, error) {
				if label != "homebrew.mxcl.redis" {
					t.Fatal("incorrect formula job", label)
				}
				return 43, true, nil
			}
			op.run = func(ctx context.Context, name string, args ...string) (string, error) {
				calls++
				verb := "stop"
				if restart {
					verb = "restart"
				}
				if name != "brew" || len(args) != 3 || args[0] != "services" || args[1] != verb || args[2] != "redis" {
					t.Fatal(name, args)
				}
				return "", nil
			}
			_, err := actionContext(context.Background(), l, p, restart, op)
			if selectedPID == 42 {
				if !errors.Is(err, ErrRefused) || calls != 0 {
					t.Fatalf("manual instance affected registered service: restart=%v err=%v calls=%d", restart, err, calls)
				}
			} else if err != nil || calls != 1 {
				t.Fatalf("owned service was not acted on: restart=%v err=%v calls=%d", restart, err, calls)
			}
		}
	}
}

func TestContextBrewRefusesChangedOrUnavailableFormulaJob(t *testing.T) {
	for _, restart := range []bool{false, true} {
		for _, mode := range []string{"changed during lookup", "missing", "lookup failure", "canceled"} {
			calls := 0
			op := fakeContextOps(&calls)
			ctx, cancel := context.WithCancel(context.Background())
			jobPID := 42
			op.brew = func(context.Context, string) (bool, error) {
				if mode == "changed during lookup" {
					jobPID = 43
				}
				return true, nil
			}
			op.launchd = func(context.Context, string) (int, bool, error) {
				if mode == "canceled" {
					cancel()
				}
				if mode == "lookup failure" {
					return 42, true, errors.New("unavailable")
				}
				return jobPID, mode != "missing", nil
			}
			_, err := actionContext(ctx, scan.Listener{PID: 42, StartTime: "stable start"}, intel.Profile{StopKind: intel.StopBrew, StopArg: "redis"}, restart, op)
			cancel()
			if calls != 0 || (mode != "canceled" && !errors.Is(err, ErrRefused)) || (mode == "canceled" && !errors.Is(err, context.Canceled)) {
				t.Fatalf("restart=%v mode=%s err=%v side effects=%d", restart, mode, err, calls)
			}
		}
	}
}
