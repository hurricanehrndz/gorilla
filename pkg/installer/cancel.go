package installer

import (
	"context"
	"errors"
	"sync"
)

// ErrCanceled is returned by Install when the user withdrew the item before
// the run started its install or uninstall command. Nothing is recorded in the
// report: the item was neither attempted nor failed.
var ErrCanceled = errors.New("canceled by user")

// Cancels lets the service withdraw items from managed runs that are already
// under way. An item can be withdrawn until a run starts its install or
// uninstall command; withdrawing it then aborts its download if one is in
// flight. A running installer command is never interrupted. The service keeps
// one Cancels for its lifetime and hands it to every run. A nil *Cancels
// withdraws nothing, which is what the command-line run uses.
type Cancels struct {
	mu        sync.Mutex
	canceled  map[string]bool
	acting    map[string]bool
	downloads map[string]context.CancelFunc
}

func NewCancels() *Cancels {
	return &Cancels{
		canceled:  make(map[string]bool),
		acting:    make(map[string]bool),
		downloads: make(map[string]context.CancelFunc),
	}
}

// Cancel withdraws name. It returns false, and changes nothing, once a run has
// started name's install or uninstall command.
func (c *Cancels) Cancel(name string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.acting[name] {
		return false
	}
	c.canceled[name] = true
	if stop := c.downloads[name]; stop != nil {
		stop()
	}
	return true
}

// Reset drops a withdrawal of name; the service calls it when a new install or
// removal of name is requested, which can happen while a run is under way. It
// leaves alone whether that run is acting on name: a cancel must still refuse
// while an installer for name runs.
func (c *Cancels) Reset(name string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.canceled, name)
}

// EndRun forgets the finished run: its withdrawals and the items it acted on.
// A cancel also reverted the self-serve selection, so later runs do not pick
// the item up again, and an item an admin manifest also requires must not stay
// skipped. An operation whose run has finished is refused as finished instead.
func (c *Cancels) EndRun() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.canceled)
	clear(c.acting)
}

func (c *Cancels) withdrawn(name string) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.canceled[name]
}

// download returns the context for name's download, cancelled if name is
// withdrawn while it runs, and the func that releases it. ok is false when name
// is already withdrawn.
func (c *Cancels) download(name string) (ctx context.Context, done func(), ok bool) {
	if c == nil {
		return context.Background(), func() {}, true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.canceled[name] {
		return nil, nil, false
	}
	ctx, stop := context.WithCancel(context.Background())
	c.downloads[name] = stop
	return ctx, func() {
		c.mu.Lock()
		delete(c.downloads, name)
		c.mu.Unlock()
		stop()
	}, true
}

// act marks name as acted on, from which point Cancel refuses it, unless name
// was withdrawn first. It is the last check before the command runs.
func (c *Cancels) act(name string) bool {
	if c == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.canceled[name] {
		return false
	}
	c.acting[name] = true
	return true
}
