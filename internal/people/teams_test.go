package people

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// reader is the organisation, as a test knows it.
type reader struct {
	mu      sync.Mutex
	members map[string][]string
	reads   map[string]int
	fail    error
}

func (r *reader) TeamMembers(_ context.Context, _, team string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reads == nil {
		r.reads = map[string]int{}
	}
	r.reads[team]++
	if r.fail != nil {
		return nil, r.fail
	}
	return r.members[team], nil
}

func (r *reader) count(team string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads[team]
}

func newTeams(t *testing.T, source *reader, now *time.Time) *Teams {
	t.Helper()
	return &Teams{
		Reader:       source,
		Organisation: "codecheckers",
		Logger:       slog.New(slog.DiscardHandler),
		Now:          func() time.Time { return *now },
	}
}

// Membership is read once and then remembered, whatever the comment traffic.
func TestATeamIsReadOncePerExpiry(t *testing.T) {
	now := time.Now()
	source := &reader{members: map[string][]string{"editors": {"nuest"}}}
	teams := newTeams(t, source, &now)

	for range 5 {
		if !teams.Has(context.Background(), "editors", "nuest") {
			t.Fatal("a member of the team was not recognised")
		}
	}
	if source.count("editors") != 1 {
		t.Errorf("the team was read %d times, want once", source.count("editors"))
	}

	// A day later the copy in hand has expired, and the next question pays for
	// a fresh one.
	now = now.Add(DefaultTTL + time.Minute)
	teams.Has(context.Background(), "editors", "nuest")
	if source.count("editors") != 2 {
		t.Errorf("the expired team was read %d times, want twice", source.count("editors"))
	}
}

// A burst of comments arriving at once is still one request.
func TestConcurrentQuestionsCostOneRead(t *testing.T) {
	now := time.Now()
	source := &reader{members: map[string][]string{"editors": {"nuest"}}}
	teams := newTeams(t, source, &now)

	var asking sync.WaitGroup
	for range 20 {
		asking.Add(1)
		go func() {
			defer asking.Done()
			teams.Has(context.Background(), "editors", "someone")
		}()
	}
	asking.Wait()

	if source.count("editors") != 1 {
		t.Errorf("the team was read %d times, want once", source.count("editors"))
	}
}

// GitHub handles are case-insensitive, and people write them as they please.
func TestAHandleIsMatchedHoweverItIsWritten(t *testing.T) {
	now := time.Now()
	teams := newTeams(t, &reader{members: map[string][]string{"editors": {"NuEsT"}}}, &now)

	for _, handle := range []string{"nuest", "NUEST", " @nuest ", "@NuEsT"} {
		if !teams.Has(context.Background(), "editors", handle) {
			t.Errorf("%q is the same editor however it is written", handle)
		}
	}
	if teams.Has(context.Background(), "editors", "someone-else") {
		t.Error("a handle outside the team is not a member")
	}
}

// A token that may not read the team makes everyone a stranger. The other
// direction - a team that cannot be read meaning everyone is an editor - would
// hand the register to anyone who can type.
func TestATeamThatCannotBeReadHasNoMembers(t *testing.T) {
	now := time.Now()
	teams := newTeams(t, &reader{fail: errors.New("403 Resource not accessible by personal access token")}, &now)

	if teams.Has(context.Background(), "editors", "nuest") {
		t.Error("an unreadable team must not make anybody an editor")
	}
	if state := teams.States("editors")[0]; state.Err == nil || state.Members != 0 {
		t.Errorf("a team that has never been read is empty, and says why: %+v", state)
	}
}

// An organisation the bot cannot reach for an hour must not turn every editor
// into a stranger: the previous answer stands until a better one arrives.
func TestAFailedRefreshKeepsTheListItHas(t *testing.T) {
	now := time.Now()
	source := &reader{members: map[string][]string{"editors": {"nuest"}}}
	teams := newTeams(t, source, &now)

	if !teams.Has(context.Background(), "editors", "nuest") {
		t.Fatal("a member of the team was not recognised")
	}

	source.fail = errors.New("502 Bad Gateway")
	now = now.Add(DefaultTTL + time.Minute)
	if !teams.Has(context.Background(), "editors", "nuest") {
		t.Error("the editor was forgotten because GitHub was unwell")
	}

	state := teams.States("editors")[0]
	if state.Err == nil || state.Members != 1 {
		t.Errorf("the stale list is not reported as stale: %+v", state)
	}
}

// The startup run: the cache is transient, the organisation's teams are the
// persistent copy, and Refresh rebuilds one from the other before anything
// asks.
func TestRefreshFillsTheCacheBeforeAnythingAsks(t *testing.T) {
	now := time.Now()
	source := &reader{members: map[string][]string{"editors": {"nuest"}, "codecheckers": {"nuest", "someone"}}}
	teams := newTeams(t, source, &now)

	states := teams.Refresh(context.Background(), "editors", "codecheckers")
	if len(states) != 2 || states[0].Members != 1 || states[1].Members != 2 {
		t.Fatalf("the reload read %+v", states)
	}

	// The first command afterwards is answered from memory.
	teams.Has(context.Background(), "editors", "nuest")
	if source.count("editors") != 1 {
		t.Errorf("the team was read %d times, want once", source.count("editors"))
	}
}

// Refresh is for an editor who has just changed a team and will not wait a day.
func TestRefreshReadsAgainWhateverTheAge(t *testing.T) {
	now := time.Now()
	source := &reader{members: map[string][]string{"editors": {"nuest"}}}
	teams := newTeams(t, source, &now)

	teams.Has(context.Background(), "editors", "nuest")
	source.mu.Lock()
	source.members["editors"] = []string{"nuest", "newcomer"}
	source.mu.Unlock()

	states := teams.Refresh(context.Background(), "editors")
	if len(states) != 1 || states[0].Members != 2 {
		t.Fatalf("the refresh read %+v", states)
	}
	if !teams.Has(context.Background(), "editors", "newcomer") {
		t.Error("the new member is not an editor after a refresh")
	}
}

// Failing closed has to be cheap. A team that cannot be read is left alone for
// a while, or a missing permission costs one doomed request per comment, on
// the path every command takes.
func TestAnUnreadableTeamIsNotAskedForOnEveryComment(t *testing.T) {
	now := time.Now()
	source := &reader{fail: errors.New("403 Resource not accessible by personal access token")}
	teams := newTeams(t, source, &now)

	for range 10 {
		if teams.Has(context.Background(), "editors", "nuest") {
			t.Fatal("an unreadable team must not make anybody an editor")
		}
	}
	if source.count("editors") != 1 {
		t.Errorf("the unreadable team was read %d times, want once", source.count("editors"))
	}

	// It is tried again before long, because a permission may be granted or a
	// bad ten minutes may pass.
	now = now.Add(RetryAfterFailure + time.Minute)
	teams.Has(context.Background(), "editors", "nuest")
	if source.count("editors") != 2 {
		t.Errorf("the team was read %d times, want twice", source.count("editors"))
	}
}

// A cache that was never wired answers like one that cannot read its teams:
// nobody holds a standing role. The nil case lives here, once, rather than in
// every caller.
func TestANilCacheHasNoMembers(t *testing.T) {
	var teams *Teams
	if teams.Has(context.Background(), "editors", "nuest") {
		t.Error("a bot with no team cache must not make anybody an editor")
	}
	if len(teams.Refresh(context.Background(), "editors")) != 0 || len(teams.States("editors")) != 0 {
		t.Error("a nil cache holds nothing")
	}
}

// A team with nobody in it has been read just as much as a full one. Treating
// empty as "never read" would re-ask GitHub on every comment - the cost the
// failure window exists to prevent, in the case failing closed is meant to
// handle gracefully.
func TestAnEmptyTeamIsStillARead(t *testing.T) {
	now := time.Now()
	source := &reader{members: map[string][]string{"editors": {}}}
	teams := newTeams(t, source, &now)

	for range 5 {
		if teams.Has(context.Background(), "editors", "nuest") {
			t.Fatal("an empty team has no members")
		}
	}
	if source.count("editors") != 1 {
		t.Errorf("the empty team was read %d times, want once", source.count("editors"))
	}
}

// A team nobody named is nobody, and is not worth asking GitHub about.
func TestAnUnnamedTeamIsNotAsked(t *testing.T) {
	now := time.Now()
	source := &reader{members: map[string][]string{"editors": {"nuest"}}}
	teams := newTeams(t, source, &now)

	if teams.Has(context.Background(), "", "nuest") {
		t.Error("an unnamed team must not make anybody an editor")
	}
	if source.count("") != 0 {
		t.Errorf("an unnamed team was asked for %d times", source.count(""))
	}
}

// A refresh says what it replaced, which is how an editor who has just changed
// a team can tell whether the copy that was in hand already had their change.
func TestARefreshSaysWhatItReplaced(t *testing.T) {
	now := time.Now()
	source := &reader{members: map[string][]string{"editors": {"nuest"}}}
	teams := newTeams(t, source, &now)

	if first := teams.Refresh(context.Background(), "editors")[0]; !first.Previous.IsZero() {
		t.Errorf("the first read replaced something: %+v", first)
	}

	when := now
	now = now.Add(2 * time.Hour)
	second := teams.Refresh(context.Background(), "editors")[0]
	if !second.Previous.Equal(when) {
		t.Errorf("the refresh says it replaced the copy from %v, want %v", second.Previous, when)
	}
}
