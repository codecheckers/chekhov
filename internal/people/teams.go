// Package people answers who somebody is.
//
// The standing roles - editor, codechecker - are a property of a person, not
// of a check, and the organisation already maintains them as GitHub teams. The
// bot holds them in a runtime-only cache: it has no storage, and needs none
// for something somebody else owns. The teams are the persistent copy, this is
// the transient one, and Refresh is how the transient one is rebuilt from the
// persistent one - at startup, or on request.
//
// The per-check roles - who is checking this paper, who wrote it - are a
// property of one issue and live in that issue. They are not here.
package people

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/codecheckers/chekhov/internal/command"
)

// DefaultTTL is how long a membership list is trusted before it is read again.
// A day: a new editor waiting a few hours is a smaller problem than a command
// that asks GitHub who everyone is every time somebody says hello.
const DefaultTTL = 24 * time.Hour

// RetryAfterFailure is how long a team that could not be read is left alone.
//
// Without it a token that may not read the teams - or an organisation having a
// bad ten minutes - costs one doomed GitHub request per comment, on the path
// every command takes. Failing closed has to be cheap, or it becomes a way to
// make the bot slow by taking a permission away.
const RetryAfterFailure = 5 * time.Minute

// errNoReader is a cache with nothing to read through: the bot has no GitHub
// credential. Everybody is a stranger, which is the safe direction.
var errNoReader = errors.New("no GitHub access to read team membership with")

// Reader is what the cache reads membership through, internal/github in
// production.
type Reader interface {
	TeamMembers(ctx context.Context, organisation, team string) ([]string, error)
}

// Teams is the standing roles, as the organisation maintains them.
//
// The zero value is not usable: Reader and Organisation have to be set. A
// Teams with no Reader answers "not a member" to everything, which is the safe
// direction - see Has.
type Teams struct {
	Reader       Reader
	Organisation string
	// TTL is how long a list is trusted, DefaultTTL when zero.
	TTL    time.Duration
	Logger *slog.Logger
	// Now is the clock, time.Now unless a test says otherwise.
	Now func() time.Time

	mu    sync.Mutex
	teams map[string]*team
}

// team is one cached membership list.
type team struct {
	// fetching serialises refreshes of this team, so that a burst of comments
	// costs one request rather than one each.
	fetching sync.Mutex

	mu      sync.Mutex
	members map[string]bool
	fetched time.Time
	// previous is when the copy replaced by the last read was fetched.
	previous time.Time
	// attempted is when the team was last asked for, successfully or not, so
	// that a team that cannot be read is not asked for again on every comment.
	attempted time.Time
	// failed is the last error, kept so that a stale list can say why it is
	// stale rather than looking merely old.
	failed error
}

// State is what the cache holds for one team, for a reply or a health check.
type State struct {
	Team    string
	Members int
	Fetched time.Time
	// Err is the last failure, nil when the list in hand is current. A State
	// with an Err and no Members was never read at all; one with both is the
	// previous answer, still standing.
	Err error
	// Previous is when the copy this read replaced was fetched, zero when
	// there was none. It is what tells an editor whether the change they just
	// made to a team had already been picked up.
	Previous time.Time
}

// Has reports whether a handle is in a team.
//
// Fail closed, in one place: a team that cannot be read has no members, so a
// token without the Members: read permission means nobody is an editor, never
// that everybody is. A nil cache - a deployment wired without one - answers
// the same way. Comparison is case-insensitive, as GitHub handles are.
func (t *Teams) Has(ctx context.Context, name, handle string) bool {
	handle = normalize(handle)
	if t == nil || handle == "" || strings.TrimSpace(name) == "" {
		return false
	}

	cached := t.fetch(ctx, name, false)
	cached.mu.Lock()
	defer cached.mu.Unlock()
	return cached.members[handle]
}

// Refresh reads the named teams now, whatever the age of what is held, and
// reports what it found.
//
// Two callers: `@chekhovbot refresh teams`, so that a membership change takes
// effect at once, and the run at startup that fills the cache before the first
// command needs it. The cache is the transient copy of something the
// organisation owns; this is how it is rebuilt from the persistent one.
func (t *Teams) Refresh(ctx context.Context, names ...string) []State {
	states := make([]State, 0, len(names))
	if t == nil {
		return states
	}
	for _, name := range names {
		states = append(states, stateOf(name, t.fetch(ctx, name, true)))
	}
	return states
}

// States is what the cache holds, for /healthz and for a reply.
func (t *Teams) States(names ...string) []State {
	states := make([]State, 0, len(names))
	if t == nil {
		return states
	}
	for _, name := range names {
		states = append(states, stateOf(name, t.team(name)))
	}
	return states
}

// stateOf reads one team's state under its lock.
func stateOf(name string, cached *team) State {
	cached.mu.Lock()
	defer cached.mu.Unlock()
	return State{Team: name, Members: len(cached.members),
		Fetched: cached.fetched, Err: cached.failed, Previous: cached.previous}
}

// fetch reads one team when what is held has expired, and returns the cached
// team either way.
//
// force is what `refresh teams` asks for: read it again whatever the age,
// because an editor who has just changed a team is watching. Without it a
// team another comment refreshed while this one waited is not read twice - one
// request per team per expiry, whatever the traffic.
func (t *Teams) fetch(ctx context.Context, name string, force bool) *team {
	cached := t.team(name)
	if !force && cached.current(t.now(), t.ttl()) {
		return cached
	}

	cached.fetching.Lock()
	defer cached.fetching.Unlock()
	// Another comment may have refreshed it while this one waited for the lock.
	if !force && cached.current(t.now(), t.ttl()) {
		return cached
	}

	if t.Reader == nil {
		cached.record(t.now(), nil, errNoReader)
		return cached
	}
	handles, err := t.Reader.TeamMembers(ctx, t.Organisation, name)
	if err != nil {
		cached.record(t.now(), nil, err)
		t.log().Warn("a team could not be read", "team", name, "error", err)
		return cached
	}
	cached.record(t.now(), handles, nil)
	return cached
}

// current reports whether the team need not be read again yet: either the list
// in hand is younger than the TTL, or the last attempt failed recently enough
// that trying again would only cost another failure.
func (c *team) current(now time.Time, ttl time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Read, not non-empty: a team with nobody in it has been read just as much
	// as a team with sixty-four, and re-reading it on every comment would cost
	// exactly what the failure window below exists to prevent.
	if !c.fetched.IsZero() && now.Sub(c.fetched) < ttl {
		return true
	}
	return c.failed != nil && now.Sub(c.attempted) < RetryAfterFailure
}

// record stores what reading the team produced. A failure keeps the previous
// membership: an organisation the bot cannot reach for an hour must not turn
// every editor into a stranger.
func (c *team) record(now time.Time, handles []string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempted, c.failed = now, err
	if err != nil {
		return
	}
	// What this read replaces, for a reply that says so. Only a successful
	// read replaces anything.
	c.previous = c.fetched
	c.members = make(map[string]bool, len(handles))
	for _, handle := range handles {
		c.members[normalize(handle)] = true
	}
	c.fetched = now
}

func (t *Teams) team(name string) *team {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.teams == nil {
		t.teams = map[string]*team{}
	}
	if t.teams[name] == nil {
		t.teams[name] = &team{}
	}
	return t.teams[name]
}

func (t *Teams) ttl() time.Duration {
	if t.TTL > 0 {
		return t.TTL
	}
	return DefaultTTL
}

func (t *Teams) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Teams) log() *slog.Logger {
	if t.Logger != nil {
		return t.Logger
	}
	return slog.Default()
}

// normalize is command.Handle, named short because this package writes it on
// every read of a membership list.
func normalize(handle string) string { return command.Handle(handle) }
