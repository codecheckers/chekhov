package check

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/codecheckers/chekhov/internal/testserver"
)

// A share link opens nothing only when Zenodo says "not there" of the record
// and of its draft. Every other answer, and none, leaves it possibly real.
func TestOnlyNoRecordAndNoDraftIsNothing(t *testing.T) {
	for name, answer := range map[string]struct {
		record, draft int
		nothing       bool
	}{
		"neither there":                      {404, 404, true},
		"both gone":                          {410, 410, true},
		"a published record, whatever token": {200, 404, false},
		"a draft its token opens":            {404, 200, false},
		"a draft it does not open":           {404, 403, false},
		"a token refused":                    {401, 404, false},
		"a bad request":                      {400, 404, false},
		"asked too often":                    {429, 429, false},
		"an outage":                          {404, 503, false},
		"no HEAD":                            {405, 405, false},
	} {
		stubbed := newStub(t)
		var asked []string
		for path, status := range map[string]int{"/zenodo/records/7": answer.record, "/zenodo/records/7/draft": answer.draft} {
			stubbed.Handle(path, func(w http.ResponseWriter, r *http.Request) {
				asked = append(asked, r.Method+" "+r.URL.RequestURI())
				w.WriteHeader(status)
			})
		}

		got := stubbed.services.ShareLinkOpensNothing(context.Background(), ZenodoTarget{ID: "7", Token: "a b&c"})

		if got != answer.nothing {
			t.Errorf("%s: %v, want %v", name, got, answer.nothing)
		}
		// The draft first, and the record only when there is no draft.
		want := []string{"HEAD /zenodo/records/7/draft?token=a+b%26c"}
		if answer.draft == 404 || answer.draft == 410 {
			want = append(want, "HEAD /zenodo/records/7?token=a+b%26c")
		}
		if !slices.Equal(asked, want) {
			t.Errorf("%s: asked %q, want %q", name, asked, want)
		}
	}

	stub := testserver.New(t)
	outside := &Services{Access: Access{HTTP: stub.Client(), Zenodo: "https://zenodo.example.invalid/api"}}
	if outside.ShareLinkOpensNothing(context.Background(), ZenodoTarget{ID: "7", Token: "t"}) {
		t.Error("Zenodo out of reach was taken to have said there is nothing")
	}
	for name, services := range map[string]*Services{"none": nil, "off": {}} {
		if services.ShareLinkOpensNothing(context.Background(), ZenodoTarget{ID: "7", Token: "t"}) {
			t.Errorf("services %s: nothing asked, and nothing found", name)
		}
	}
}
