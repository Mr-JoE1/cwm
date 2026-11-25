package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Mr-JoE1/cwm/internal/agent"
	"github.com/Mr-JoE1/cwm/internal/storage"
)

// agentStopFirst is the refusal when a running agent cannot be paused.
const agentStopFirst = "the cwm agent is running — stop it first: cwm service stop (start it again afterwards: cwm service start)"

// holdAgent keeps the agent's maintenance tick away from the files a
// migration or rollback moves (D101), and returns the func that lets it
// go. No agent running: hold its single-instance lock, so none starts
// half-way. An agent running: pause it through /api/agent/pause (a lease
// it renews; the agent resumes by itself if this process dies). An agent
// that does not answer, or one too old to pause: refuse, naming the stop
// command. A seam for tests.
var holdAgent = func(e *Env, layout *storage.Layout, why string) (func(), error) {
	root := layout.Root
	lock, err := agent.Acquire(root)
	if err == nil {
		return func() { _ = lock.Release() }, nil
	}
	if !errors.Is(err, agent.ErrLocked) {
		return nil, err
	}
	ctx := context.Background()
	c, err := agent.Load(root)
	if err == nil {
		_, err = agent.Probe(ctx, c)
	}
	if err == nil {
		release, perr := agent.RemotePause(ctx, c, fmt.Sprintf("cli-%d", os.Getpid()), why)
		if perr == nil {
			fmt.Fprintln(e.Stdout, "agent: maintenance paused while files move (it resumes afterwards)")
			return release, nil
		}
		if errors.Is(perr, agent.ErrPaused) {
			return nil, fmt.Errorf("another migration is running (%v) — wait for it to finish", perr)
		}
		err = perr
	}
	return nil, fmt.Errorf("%s (%v)", agentStopFirst, err)
}
