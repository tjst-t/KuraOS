package cmdexec

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Fake is a deterministic Executor used by unit tests. A test registers
// canned responses keyed by "command + args joined by space" and Run looks
// them up. Unknown commands return an error so a missing test fixture is
// loud rather than silently producing empty output.
//
// The keying strategy (string-join) is deliberately simple — engines under
// test typically issue a small fixed set of CLI invocations, and the join
// makes test setup readable: "zpool list -H -p" maps to a known fixture.
type Fake struct {
	mu        sync.Mutex
	responses map[string]FakeResponse
	calls     []FakeCall
}

// FakeResponse is the canned output the Fake returns for a matched call.
type FakeResponse struct {
	Stdout []byte
	Stderr []byte
	Err    error
}

// FakeCall is a record of one invocation, kept so tests can assert on call
// order and arguments after Run completes.
type FakeCall struct {
	Name string
	Args []string
}

// NewFake returns an empty Fake. Tests register expectations with Register
// before calling code that consumes the Executor.
func NewFake() *Fake {
	return &Fake{responses: make(map[string]FakeResponse)}
}

// Register binds a canned response to a "name + args" key. If a key is
// registered twice, the later registration wins — useful for test setup
// where a default is overridden in a sub-table case.
func (f *Fake) Register(name string, args []string, resp FakeResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[fakeKey(name, args)] = resp
}

// RegisterStdout is a convenience for the common success-path case.
func (f *Fake) RegisterStdout(name string, args []string, stdout []byte) {
	f.Register(name, args, FakeResponse{Stdout: stdout})
}

// Run looks up the canned response matching name+args. An unregistered call
// returns an error rather than empty output so tests fail loudly when a code
// path issues an unexpected command.
func (f *Fake) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, FakeCall{Name: name, Args: append([]string(nil), args...)})
	resp, ok := f.responses[fakeKey(name, args)]
	if !ok {
		return nil, nil, fmt.Errorf("cmdexec.Fake: no response registered for %s %v", name, args)
	}
	return resp.Stdout, resp.Stderr, resp.Err
}

// Calls returns a copy of the call log, in invocation order.
func (f *Fake) Calls() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]FakeCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func fakeKey(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	return name + " " + strings.Join(args, " ")
}
