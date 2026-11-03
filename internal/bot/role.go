package bot

import (
	"context"
	"sync"
	"time"

	"github.com/codecheckers/chekhov/internal/github"
)

// roleReader says what the bot's own account may do on the register. The
// GitHub client is one; the tests stub it.
type roleReader interface {
	Role(ctx context.Context) (string, error)
}

// roleState is what the startup check found, as /healthz reports it: nil
// until the check has answered.
type roleState struct {
	mu    sync.Mutex
	entry map[string]any
}

func (r *roleState) set(role string, err error) {
	entry := map[string]any{"read": time.Now().UTC().Format(time.RFC3339)}
	if err != nil {
		entry["error"] = err.Error()
	} else {
		entry["role"], entry["writes"] = role, github.Writes(role)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entry = entry
}

func (r *roleState) state() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entry
}

// checkRole asks, once at startup, whether the bot's account may write to the
// register, and says so in the log when it may not.
//
// The token's page cannot answer this: a fine-grained token may do only what
// the account behind it may do as well, and an account that is `read` on the
// register turns a token with every write permission into one that can only
// comment. codecheckers/register#238 found it that way, three commands in, from
// three 403s in the log; this puts it in the first lines after a deploy. See
// docs/github-token.md.
//
// In the background, as the team load is, because the platform's health check
// must not wait for GitHub.
func (s *Server) checkRole(ctx context.Context) {
	defer s.recoverBackground("The check of the bot's role at startup panicked.")

	reader, ok := s.Replies.(roleReader)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	role, err := reader.Role(ctx)
	s.role.set(role, err)
	log := s.Logger.With("register", s.Settings.TargetRepository())
	switch {
	case err != nil:
		log.Warn("the bot's role on the register could not be read", "error", err)
	case !github.Writes(role):
		// What a role below write cannot do is github.Writes's to list.
		log.Warn("the bot's account cannot write to the register, whatever the token allows: "+
			"its writes will be refused with a 403. Give the account write on the repository, "+
			"see docs/github-token.md", "account", s.Settings.BotUser(), "role", role)
	default:
		log.Info("the bot's role on the register", "role", role)
	}
}
