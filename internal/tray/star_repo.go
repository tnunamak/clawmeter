package tray

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// starRepoSlug is the repository the Settings item stars.
const starRepoSlug = "tnunamak/clawmeter"

const (
	// starCheckTimeout bounds the whole check: gh on PATH, auth, and the
	// starred lookup. Past it the item stays hidden.
	starCheckTimeout = 5 * time.Second
	// starWriteTimeout bounds the one click that stars the repository.
	starWriteTimeout = 15 * time.Second
	// starRecheckAfter is how old a check may get before Settings or a
	// status refresh runs it again.
	starRecheckAfter = 10 * time.Minute
)

// ghRunner runs gh with args and returns what it wrote to stdout and stderr.
type ghRunner func(ctx context.Context, args ...string) (stdout, stderr []byte, err error)

// starRepo decides whether Settings offers "Star clawmeter on GitHub". It
// shells out to gh only; it never reads or stores a token.
type starRepo struct {
	run      ghRunner
	lookPath func(string) (string, error)
	timeout  time.Duration
	now      func() time.Time
	setShown func(shown bool)

	mu        sync.Mutex
	checking  bool
	checkedAt time.Time
	starred   bool // set by a successful click; keeps the item hidden for the session
	starring  bool
}

func newStarRepo(setShown func(shown bool)) *starRepo {
	return &starRepo{
		run:      runGH,
		lookPath: exec.LookPath,
		timeout:  starCheckTimeout,
		now:      time.Now,
		setShown: setShown,
	}
}

// Check finds out whether to offer the star item and applies the answer. It
// blocks for up to the check timeout, so callers run it in a goroutine. A
// check that is already running makes this a no-op.
func (r *starRepo) Check() {
	r.mu.Lock()
	if r.checking {
		r.mu.Unlock()
		return
	}
	r.checking = true
	r.mu.Unlock()

	offer := r.offer()

	r.mu.Lock()
	r.checking = false
	r.checkedAt = r.now()
	shown := offer && !r.starred
	r.mu.Unlock()
	r.setShown(shown)
}

// RecheckIfStale runs Check again when the last check is older than
// starRecheckAfter. Callers run it in a goroutine.
func (r *starRepo) RecheckIfStale() {
	r.mu.Lock()
	stale := !r.checking && !r.checkedAt.IsZero() && r.now().Sub(r.checkedAt) > starRecheckAfter
	r.mu.Unlock()
	if stale {
		r.Check()
	}
}

// Star stars the repository as the gh user. On success it hides the item for
// the rest of the session. On failure it leaves the item shown and returns
// gh's reason.
func (r *starRepo) Star() error {
	r.mu.Lock()
	if r.starring {
		r.mu.Unlock()
		return nil
	}
	r.starring = true
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), starWriteTimeout)
	defer cancel()
	_, stderr, err := r.run(ctx, "api", "-X", "PUT", "user/starred/"+starRepoSlug)

	r.mu.Lock()
	r.starring = false
	if err != nil {
		r.mu.Unlock()
		return errors.New(ghReason(err, stderr))
	}
	r.starred = true
	r.mu.Unlock()
	r.setShown(false)
	return nil
}

// offer reports whether gh is installed, signed in, and the repository is not
// starred yet. Any other outcome, including an error or a timeout, is false.
func (r *starRepo) offer() bool {
	if _, err := r.lookPath("gh"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	if _, _, err := r.run(ctx, "auth", "status"); err != nil {
		return false
	}
	// gh api exits 0 on 204 (starred) and 1 with "HTTP 404" on 404 (not starred).
	_, stderr, err := r.run(ctx, "api", "user/starred/"+starRepoSlug)
	if err == nil {
		return false
	}
	return bytes.Contains(stderr, []byte("HTTP 404"))
}

// ghReason is the first line gh wrote to stderr, or the error text when gh
// wrote nothing.
func ghReason(err error, stderr []byte) string {
	for _, line := range strings.Split(string(stderr), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return err.Error()
}

// runGH runs the gh on PATH. It never reads gh's output beyond the stderr
// reason, and it turns prompts off so a check cannot wait for input.
func runGH(ctx context.Context, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	hideGHWindow(cmd)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1")
	cmd.WaitDelay = time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		err = fmt.Errorf("gh %s: %w", args[0], ctx.Err())
	}
	return stdout.Bytes(), stderr.Bytes(), err
}
